package testflight

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/londek/ipadecrypt/internal/config"
	"github.com/londek/ipadecrypt/internal/device"
	"howett.net/plist"
	_ "modernc.org/sqlite"
)

const (
	testFlightDataPrefix = "/var/mobile/Containers/Data/Application/"
	maxRequestArchive    = 1 << 20
	maxCacheFile         = 128 << 20
)

type cachedRequest struct {
	url     string
	archive []byte
}

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

	var (
		requests []cachedRequest
		queryErr error
	)

	for _, candidate := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if !strings.HasPrefix(candidate, dataRoot+"/Library/Caches/") || !strings.HasSuffix(candidate, "/Cache.db") || strings.ContainsAny(candidate, "\r\x00") {
			continue
		}

		requests, queryErr = queryCacheSnapshot(ctx, client, candidate)
		if queryErr == nil && len(requests) != 0 {
			break
		}
	}

	if len(requests) == 0 {
		if queryErr != nil {
			return Session{}, queryErr
		}

		return Session{}, errors.New("no authenticated TestFlight session found; sign in and open TestFlight on the device once")
	}

	var identifyErr error

	for _, request := range requests {
		session, err := sessionFromCachedRequest(request.url, request.archive)
		if err == nil {
			return session, nil
		}

		identifyErr = err
	}

	return Session{}, fmt.Errorf("identify TestFlight session: %w", identifyErr)
}

func queryCacheSnapshot(ctx context.Context, client *device.Client, remotePath string) ([]cachedRequest, error) {
	temporary, err := os.MkdirTemp("", "ipadecrypt-testflight-cache-*")
	if err != nil {
		return nil, fmt.Errorf("create TestFlight cache snapshot: %w", err)
	}
	defer os.RemoveAll(temporary)

	localPath := filepath.Join(temporary, "Cache.db")
	if err := downloadSnapshotFile(client, remotePath, localPath, true); err != nil {
		return nil, err
	}

	for _, suffix := range []string{"-wal", "-shm"} {
		if err := downloadSnapshotFile(client, remotePath+suffix, localPath+suffix, false); err != nil {
			return nil, err
		}
	}

	return readCacheSnapshot(ctx, localPath)
}

func readCacheSnapshot(ctx context.Context, path string) ([]cachedRequest, error) {
	database, err := sql.Open("sqlite", readOnlySQLiteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open TestFlight cache snapshot: %w", err)
	}
	defer database.Close()

	database.SetMaxOpenConns(1)

	const query = `
		SELECT request_key, request_object
		FROM cfurl_cache_response
		JOIN cfurl_cache_blob_data USING(entry_ID)
		WHERE request_key GLOB 'https://testflight.apple.com/v*/accounts/*'
		  AND length(request_key) BETWEEN 1 AND 4096
		  AND length(request_object) BETWEEN 1 AND ?
		ORDER BY entry_ID DESC
		LIMIT 20`

	rows, err := database.QueryContext(ctx, query, maxRequestArchive)
	if err != nil {
		return nil, fmt.Errorf("query TestFlight cache snapshot: %w", err)
	}
	defer rows.Close()

	requests := make([]cachedRequest, 0, 20)

	for rows.Next() {
		var request cachedRequest
		if err := rows.Scan(&request.url, &request.archive); err != nil {
			return nil, fmt.Errorf("read TestFlight cache snapshot: %w", err)
		}

		requests = append(requests, request)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read TestFlight cache snapshot: %w", err)
	}

	return requests, nil
}

func readOnlySQLiteDSN(path string) string {
	slashPath := filepath.ToSlash(path)
	if len(slashPath) >= 2 && slashPath[1] == ':' {
		slashPath = "/" + slashPath
	}

	return (&url.URL{Scheme: "file", Path: slashPath, RawQuery: "mode=ro"}).String()
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
