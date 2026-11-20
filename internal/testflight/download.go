package testflight

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/londek/ipadecrypt/internal/appstore"
	"howett.net/plist"
)

const packageHost = "iosapps.itunes.apple.com"

var identityToken = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type Download struct {
	Ticket   appstore.DownloadTicket
	BundleID string
	Version  string
	BuildID  int64
}

type installResponse struct {
	Data installData `json:"data"`
}

type installData struct {
	URL            string         `json:"url"`
	Sinf           string         `json:"sinf"`
	DPInfo         string         `json:"dpInfo"`
	PackageSize    int64          `json:"packageSize"`
	ITunesMetadata map[string]any `json:"iTunesMetadata"`
	Framework      framework      `json:"framework"`
	Packages       packages       `json:"packages"`
}

type framework struct {
	BundleID     string `json:"bundleId"`
	Version      string `json:"shortVersion"`
	BuildVersion string `json:"bundleVersion"`
}

type packages struct {
	Full fullPackage `json:"full"`
}

type fullPackage struct {
	URL         string `json:"url"`
	Sinf        string `json:"sinf"`
	DPInfo      string `json:"dpInfo"`
	PackageSize int64  `json:"packageSizeBytes"`
}

type sinfEnvelope struct {
	Sinfs []appstore.Sinf `plist:"sinfs"`
}

func (c *Client) PrepareDownload(ctx context.Context, appID int64, requestedVersion, deviceID string) (Download, error) {
	deviceID = strings.TrimSpace(deviceID)
	hardwareID, err := testFlightHardwareID(deviceID)
	if err != nil {
		return Download{}, errors.New("TestFlight download needs the source device UDID; pass --udid")
	}

	versions, err := c.ListVersions(ctx, appID)
	if err != nil {
		return Download{}, err
	}

	version, err := selectVersion(versions, requestedVersion)
	if err != nil {
		return Download{}, err
	}

	builds, err := c.listBuilds(ctx, appID, version)
	if err != nil {
		return Download{}, err
	}

	if len(builds) == 0 {
		return Download{}, fmt.Errorf("no active TestFlight build is available for version %s", version)
	}

	build := builds[0]

	dsid, err := strconv.ParseUint(c.dsid, 10, 64)
	if err != nil {
		return Download{}, errors.New("invalid App Store account identifier")
	}

	kbsync, err := c.store.TestFlightKBSync(ctx, dsid)
	if err != nil {
		return Download{}, fmt.Errorf("prepare TestFlight FairPlay request: %w", err)
	}

	payload := map[string]any{
		"kbSync":           kbsync,
		"udid":             deviceID,
		"deviceName":       "ipadecrypt",
		"storefrontId":     c.storefront,
		"isFromAutoUpdate": false,
	}
	path := c.accountPath() + "/apps/" + strconv.FormatInt(appID, 10) +
		"/builds/" + strconv.FormatInt(build.ID, 10) + "/install"

	var response installResponse
	if err := c.send(ctx, http.MethodPost, path, payload, &response); err != nil {
		return Download{}, err
	}

	ticket, identity, err := parseInstallData(response.Data)
	if err != nil {
		return Download{}, err
	}
	ticket.HardwareID = hardwareID

	if identity.Version != version {
		return Download{}, fmt.Errorf("TestFlight returned version %q instead of %q", identity.Version, version)
	}

	if build.BundleID != "" && identity.BundleID != build.BundleID {
		return Download{}, errors.New("TestFlight returned a different app bundle")
	}

	return Download{Ticket: ticket, BundleID: identity.BundleID, Version: identity.Version, BuildID: build.ID}, nil
}

func testFlightHardwareID(deviceID string) ([]byte, error) {
	if len(deviceID) != 40 || strings.ContainsAny(deviceID, "\r\n") {
		return nil, errors.New("TestFlight device UDID must contain 40 hexadecimal digits")
	}

	hardwareID, err := hex.DecodeString(deviceID)
	if err != nil {
		return nil, errors.New("TestFlight device UDID must contain 40 hexadecimal digits")
	}

	return hardwareID, nil
}

func parseInstallData(data installData) (appstore.DownloadTicket, framework, error) {
	packageURL := data.Packages.Full.URL
	if packageURL == "" {
		packageURL = data.URL
	}

	if err := validatePackageURL(packageURL); err != nil {
		return appstore.DownloadTicket{}, framework{}, err
	}

	encodedSinfs := data.Packages.Full.Sinf
	if encodedSinfs == "" {
		encodedSinfs = data.Sinf
	}

	if encodedSinfs == "" {
		return appstore.DownloadTicket{}, framework{}, errors.New("TestFlight download contains no SINF")
	}

	var envelope sinfEnvelope
	if _, err := plist.Unmarshal([]byte(encodedSinfs), &envelope); err != nil {
		return appstore.DownloadTicket{}, framework{}, fmt.Errorf("decode TestFlight SINF: %w", err)
	}

	if len(envelope.Sinfs) == 0 {
		return appstore.DownloadTicket{}, framework{}, errors.New("TestFlight download contains no decoded SINF")
	}

	encodedDPInfo := data.Packages.Full.DPInfo
	if encodedDPInfo == "" {
		encodedDPInfo = data.DPInfo
	}

	dpInfo, err := base64.StdEncoding.Strict().DecodeString(encodedDPInfo)
	if err != nil {
		return appstore.DownloadTicket{}, framework{}, fmt.Errorf("decode TestFlight dpInfo: %w", err)
	}

	if len(dpInfo) != 88 {
		return appstore.DownloadTicket{}, framework{}, errors.New("TestFlight download contains invalid dpInfo")
	}

	if data.Framework.BundleID == "" || data.Framework.Version == "" || data.Framework.BuildVersion == "" ||
		!identityToken.MatchString(data.Framework.BundleID) || !identityToken.MatchString(data.Framework.Version) ||
		!identityToken.MatchString(data.Framework.BuildVersion) {
		return appstore.DownloadTicket{}, framework{}, errors.New("TestFlight download has an invalid app identity")
	}

	packageSize := data.Packages.Full.PackageSize
	if packageSize <= 0 {
		packageSize = data.PackageSize
	}

	metadata := data.ITunesMetadata
	if metadata == nil {
		metadata = make(map[string]any)
	}

	metadata["bundleShortVersionString"] = data.Framework.Version
	metadata["bundleVersion"] = data.Framework.BuildVersion
	metadata["bundleIdentifier"] = data.Framework.BundleID

	return appstore.DownloadTicket{
		URL:      packageURL,
		Sinfs:    envelope.Sinfs,
		DPInfo:   dpInfo,
		Metadata: metadata,
		AssetInfo: map[string]any{
			"file-size": packageSize,
		},
	}, data.Framework, nil
}

func validatePackageURL(raw string) error {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Scheme != "https" || strings.ToLower(parsed.Hostname()) != packageHost || parsed.Port() != "" ||
		parsed.User != nil || parsed.Path == "" || parsed.RawQuery == "" || parsed.Fragment != "" {
		return errors.New("TestFlight returned an invalid package URL")
	}

	return nil
}
