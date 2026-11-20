package testflight

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/londek/ipadecrypt/internal/config"
	"github.com/londek/ipadecrypt/internal/device"
	"howett.net/plist"
)

const (
	testFlightDataPrefix = "/var/mobile/Containers/Data/Application/"
	maxRequestArchive    = 1 << 20
	maxCacheFile         = 128 << 20
)

// ImportSession reads a cached account request from TestFlight on a jailbroken
// device. It does not launch or instrument TestFlight and does not capture an
// install response.
func ImportSession(ctx context.Context, deviceConfig config.Device) (Session, error) {
	client, err := device.Connect(ctx, deviceConfig)
	if err != nil {
		return Session{}, fmt.Errorf("connect to TestFlight device: %w", err)
	}
	defer client.Close()

	stdout, stderr, code, err := client.Run("find /var/mobile/Containers/Data/Application -path '*/Library/Application Support/TestFlight.sqlite' -print -quit 2>/dev/null")
	if err != nil {
		return Session{}, fmt.Errorf("locate TestFlight data: %w", err)
	}

	if code != 0 {
		return Session{}, fmt.Errorf("locate TestFlight data: exit %d: %s", code, strings.TrimSpace(stderr))
	}

	databasePath := strings.TrimSpace(stdout)

	const databaseSuffix = "/Library/Application Support/TestFlight.sqlite"
	if !strings.HasPrefix(databasePath, testFlightDataPrefix) || !strings.HasSuffix(databasePath, databaseSuffix) || strings.ContainsAny(databasePath, "\r\n\x00") {
		return Session{}, errors.New("could not locate the TestFlight data container")
	}

	dataRoot := strings.TrimSuffix(databasePath, databaseSuffix)

	stdout, stderr, code, err = client.Run("find " + shellQuote(dataRoot+"/Library/Caches") + " -name Cache.db -type f -print 2>/dev/null")
	if err != nil {
		return Session{}, fmt.Errorf("locate TestFlight cache: %w", err)
	}

	if code != 0 {
		return Session{}, fmt.Errorf("locate TestFlight cache: exit %d: %s", code, strings.TrimSpace(stderr))
	}

	query := "SELECT request_key || char(9) || hex(request_object) FROM cfurl_cache_response JOIN cfurl_cache_blob_data USING(entry_ID) WHERE request_key GLOB 'https://testflight.apple.com/v*/accounts/*' ORDER BY entry_ID DESC LIMIT 20;"

	var (
		row      string
		queryErr error
	)

	for _, candidate := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if !strings.HasPrefix(candidate, dataRoot+"/Library/Caches/") || !strings.HasSuffix(candidate, "/Cache.db") || strings.ContainsAny(candidate, "\r\x00") {
			continue
		}

		row, queryErr = queryCacheSnapshot(ctx, client, candidate, query)
		if queryErr == nil && strings.TrimSpace(row) != "" {
			break
		}
	}

	if strings.TrimSpace(row) == "" {
		if queryErr != nil {
			return Session{}, queryErr
		}

		return Session{}, errors.New("no authenticated TestFlight session found; sign in and open TestFlight on the device once")
	}

	var identifyErr error

	for _, candidate := range strings.Split(strings.TrimSpace(row), "\n") {
		session, err := sessionFromCacheRow(candidate)
		if err == nil {
			return session, nil
		}

		identifyErr = err
	}

	return Session{}, fmt.Errorf("identify TestFlight session: %w", identifyErr)
}

func sessionFromCacheRow(row string) (Session, error) {
	requestURL, encoded, ok := strings.Cut(strings.TrimSpace(row), "\t")
	if !ok || len(encoded) == 0 || len(encoded) > maxRequestArchive*2 {
		return Session{}, errors.New("TestFlight cache contains no usable account request")
	}

	archive, err := hex.DecodeString(encoded)
	if err != nil {
		return Session{}, errors.New("TestFlight cache contains an invalid request archive")
	}

	return sessionFromCachedRequest(requestURL, archive)
}

func queryCacheSnapshot(ctx context.Context, client *device.Client, remotePath, query string) (string, error) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		return "", errors.New("TestFlight session import needs sqlite3 on the computer")
	}

	temporary, err := os.MkdirTemp("", "ipadecrypt-testflight-cache-*")
	if err != nil {
		return "", fmt.Errorf("create TestFlight cache snapshot: %w", err)
	}
	defer os.RemoveAll(temporary)

	localPath := filepath.Join(temporary, "Cache.db")
	if err := downloadSnapshotFile(client, remotePath, localPath, true); err != nil {
		return "", err
	}

	for _, suffix := range []string{"-wal", "-shm"} {
		if err := downloadSnapshotFile(client, remotePath+suffix, localPath+suffix, false); err != nil {
			return "", err
		}
	}

	command := exec.CommandContext(ctx, sqlite, "-readonly", localPath, query)

	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("query TestFlight cache snapshot: %w", err)
	}

	return string(output), nil
}

func downloadSnapshotFile(client *device.Client, remotePath, localPath string, required bool) error {
	info, err := client.Stat(remotePath)
	if err != nil {
		if !required && errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return fmt.Errorf("stat TestFlight cache: %w", err)
	}

	if info.Size() < 0 || info.Size() > maxCacheFile {
		return errors.New("TestFlight cache file is unexpectedly large")
	}

	file, err := os.OpenFile(localPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create TestFlight cache snapshot: %w", err)
	}

	if err := client.Download(remotePath, file); err != nil {
		_ = file.Close()
		return fmt.Errorf("download TestFlight cache snapshot: %w", err)
	}

	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("finish TestFlight cache snapshot: %w", err)
	}

	if err := file.Close(); err != nil {
		return fmt.Errorf("close TestFlight cache snapshot: %w", err)
	}

	return nil
}

func sessionFromCachedRequest(requestURL string, archive []byte) (Session, error) {
	if len(requestURL) == 0 || len(requestURL) > 4096 {
		return Session{}, errors.New("TestFlight cache contains an invalid account URL")
	}

	parsed, err := url.ParseRequestURI(requestURL)
	if err != nil || parsed.Scheme != "https" || strings.ToLower(parsed.Hostname()) != "testflight.apple.com" ||
		parsed.Port() != "" || parsed.Fragment != "" {
		return Session{}, errors.New("TestFlight cache contains an invalid account URL")
	}

	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 3 || len(parts[0]) != 2 || parts[0][0] != 'v' || parts[0][1] < '1' || parts[0][1] > '9' ||
		parts[1] != "accounts" || parts[2] == "" || len(parts[2]) > 512 ||
		strings.ContainsAny(parts[2], "\r\n\t") {
		return Session{}, errors.New("TestFlight cache contains an invalid account path")
	}

	var root map[string]any
	if _, err := plist.Unmarshal(archive, &root); err != nil {
		return Session{}, fmt.Errorf("decode TestFlight request archive: %w", err)
	}

	array, ok := root["Array"].([]any)
	if !ok {
		return Session{}, errors.New("TestFlight request archive has an unexpected shape")
	}

	var sessionID string

	for _, item := range array {
		headers, ok := item.(map[string]any)
		if !ok {
			continue
		}

		for name, rawValue := range headers {
			if !strings.EqualFold(name, "X-Session-Id") {
				continue
			}

			candidate, ok := rawValue.(string)
			if !ok || !validSessionID(candidate) {
				continue
			}

			if sessionID != "" && sessionID != candidate {
				return Session{}, errors.New("TestFlight request archive contains conflicting sessions")
			}

			sessionID = candidate
		}
	}

	if sessionID == "" {
		return Session{}, errors.New("TestFlight request archive contains no valid session")
	}

	return Session{SessionID: sessionID, AccountID: parts[2]}, nil
}

func validSessionID(value string) bool {
	if value == "" || len(value) > 4096 {
		return false
	}

	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}

	return true
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
