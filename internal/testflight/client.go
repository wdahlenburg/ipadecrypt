package testflight

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // TestFlight's protocol requires SHA-1.
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/londek/ipadecrypt/internal/appstore"
)

const (
	apiOrigin     = "https://testflight.apple.com"
	maxResponse   = 10 << 20
	sessionSalt   = "LWzxL71nAUJTbK9X54DDQU5KC6oTcBFuibEJXIZglPRkhCSmh68PREebiNDZXctOKXpIcOfbnndI2FjMveKfRYOLPfYFRTgua1vg"
	deviceModel   = "iPhone10,1"
	testFlightUA  = "Oasis/4.4.1 OasisBuild/697.1 iOS/16.7.10 model/iPhone10,1 hwp/t8015 build/20H350 (6; dt:157) AMS/1 TSE/0"
	testDeviceSet = "iPhone10,1 iPhone9,1"
)

var ErrSessionExpired = errors.New("TestFlight session expired")

type Client struct {
	store      *appstore.Client
	http       *http.Client
	dsid       string
	storefront string
	sessionID  string
	accountID  string
}

type Session struct {
	SessionID string
	AccountID string
}

type App struct {
	ID            int64  `json:"appId"`
	Name          string `json:"name"`
	DeveloperName string `json:"developerName"`
}

func New(store *appstore.Client, account *appstore.Account, session Session) (*Client, error) {
	if store == nil || account == nil {
		return nil, errors.New("TestFlight requires an App Store client and account")
	}

	if dsid, err := strconv.ParseUint(account.DirectoryServicesID, 10, 64); err != nil || dsid == 0 {
		return nil, errors.New("TestFlight requires a numeric App Store account identifier")
	}

	if account.StoreFront == "" {
		return nil, errors.New("TestFlight requires an App Store storefront")
	}

	return &Client{
		store:      store,
		http:       newHTTPClient(),
		dsid:       account.DirectoryServicesID,
		storefront: account.StoreFront,
		sessionID:  session.SessionID,
		accountID:  session.AccountID,
	}, nil
}

func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (c *Client) ListApps(ctx context.Context) ([]App, error) {
	if c.sessionID == "" || c.accountID == "" {
		return nil, ErrSessionExpired
	}

	path := "/v4/accounts/" + url.PathEscape(c.accountID) + "/apps"

	var response struct {
		Data struct {
			Apps []App `json:"apps"`
		} `json:"data"`
	}
	if err := c.send(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}

	sort.Slice(response.Data.Apps, func(i, j int) bool {
		if response.Data.Apps[i].DeveloperName != response.Data.Apps[j].DeveloperName {
			return response.Data.Apps[i].DeveloperName < response.Data.Apps[j].DeveloperName
		}

		if response.Data.Apps[i].Name != response.Data.Apps[j].Name {
			return response.Data.Apps[i].Name < response.Data.Apps[j].Name
		}

		return response.Data.Apps[i].ID < response.Data.Apps[j].ID
	})

	return response.Data.Apps, nil
}

func (c *Client) send(ctx context.Context, method, path string, payload any, output any) error {
	endpoint := apiOrigin + path

	requestID, err := newUUID()
	if err != nil {
		return err
	}

	proof, err := c.store.TestFlightAMDHeaders(ctx, endpoint)
	if err != nil {
		return fmt.Errorf("prepare TestFlight request: %w", err)
	}

	var body []byte
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode TestFlight request: %w", err)
		}
	}

	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create TestFlight request: %w", err)
	}

	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Language", "en")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", testFlightUA)
	request.Header.Set("X-Apple-Device-Model", deviceModel)
	request.Header.Set("X-Apple-Store-Front", c.storefront)
	request.Header.Set("X-Apple-TA-Device", testDeviceSet)
	request.Header.Set("X-Request-Id", requestID)
	request.Header.Set("X-Session-Id", c.sessionID)
	request.Header.Set("X-Session-Digest", sessionDigest(c.sessionID, requestID, c.dsid))

	for name, value := range proof {
		request.Header.Set(name, value)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("send TestFlight request: %w", err)
	}
	defer response.Body.Close()

	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil {
		return fmt.Errorf("read TestFlight response: %w", err)
	}

	if len(data) > maxResponse {
		return errors.New("TestFlight response is unexpectedly large")
	}

	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return ErrSessionExpired
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return responseError(response.StatusCode, data)
	}

	if output == nil {
		return nil
	}

	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode TestFlight response: %w", err)
	}

	return nil
}

func sessionDigest(sessionID, requestID, dsid string) string {
	digest := sha1.Sum([]byte(sessionID + requestID + dsid + sessionSalt))
	return hex.EncodeToString(digest[:])
}

func responseError(status int, body []byte) error {
	var response struct {
		Messages []struct {
			Code string `json:"code"`
		} `json:"messages"`
	}

	_ = json.Unmarshal(body, &response)

	codes := make([]string, 0, len(response.Messages))
	for _, message := range response.Messages {
		code := strings.TrimSpace(message.Code)
		if code != "" && len(code) <= 128 && !strings.ContainsAny(code, "\r\n") {
			codes = append(codes, code)
		}
	}

	if len(codes) != 0 {
		return fmt.Errorf("TestFlight returned HTTP %d (%s)", status, strings.Join(codes, ", "))
	}

	return fmt.Errorf("TestFlight returned HTTP %d", status)
}

func newUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate request identifier: %w", err)
	}

	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80

	return fmt.Sprintf("%08X-%04X-%04X-%04X-%012X",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
