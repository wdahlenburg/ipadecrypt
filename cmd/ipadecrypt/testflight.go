package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/londek/ipadecrypt/internal/appstore"
	"github.com/londek/ipadecrypt/internal/config"
	"github.com/londek/ipadecrypt/internal/fairplay"
	"github.com/londek/ipadecrypt/internal/pipeline"
	"github.com/londek/ipadecrypt/internal/testflight"
	"github.com/londek/ipadecrypt/internal/tui"
	"github.com/spf13/cobra"
)

func testFlightAppsHandler(cmd *cobra.Command, _ []string) {
	cfg, paths, err := loadConfigOrDefault(rootDirOverride)
	if err != nil {
		tui.Err("%v", err)
		return
	}

	_, _, apps, err := openTestFlight(cmd.Context(), cfg, paths)
	if err != nil {
		tui.Err("list TestFlight apps: %v", err)
		return
	}

	if len(apps) == 0 {
		tui.Info("no TestFlight apps are available for this account")
		return
	}

	developer := ""
	for _, app := range apps {
		if app.DeveloperName != developer {
			developer = app.DeveloperName
			tui.Heading("%s", developer)
		}

		tui.Item("%s (%d)", app.Name, app.ID)
	}
}

func testFlightLinkHandler(cmd *cobra.Command, args []string) {
	code, err := testflight.ParsePublicLink(args[0])
	if err != nil {
		tui.Err("%v", err)
		return
	}

	cfg, paths, err := loadConfigOrDefault(rootDirOverride)
	if err != nil {
		tui.Err("%v", err)
		return
	}

	_, client, _, err := openTestFlight(cmd.Context(), cfg, paths)
	if err != nil {
		tui.Err("connect to TestFlight: %v", err)
		return
	}

	link, err := client.ResolvePublicLink(cmd.Context(), code)
	if err != nil {
		tui.Err("preview TestFlight link: %v", err)
		return
	}

	renderTestFlightLink(link)

	if !testFlightLinkAccept {
		return
	}

	if err := link.AvailabilityError(); err != nil {
		tui.Err("accept TestFlight link: %v", err)
		return
	}

	if err := client.AcceptPublicLink(cmd.Context(), code); err != nil {
		tui.Err("accept TestFlight link: %v", err)
		return
	}

	tui.OK("accepted %s", link.App.Name)
}

func renderTestFlightLink(link testflight.PublicLink) {
	tui.Heading("%s", link.App.ProviderName)
	tui.Item("%s (%d)", link.App.Name, link.App.ID)

	if err := link.AvailabilityError(); err != nil {
		tui.Warn("%v", err)
		return
	}

	if warning := link.CompatibilityWarning(); warning != "" {
		tui.Warn("%s", warning)
	}
}

func testFlightVersionsHandler(cmd *cobra.Command, args []string) {
	appID, err := parseTestFlightAppID(args[0])
	if err != nil {
		tui.Err("%v", err)
		return
	}

	cfg, paths, err := loadConfigOrDefault(rootDirOverride)
	if err != nil {
		tui.Err("%v", err)
		return
	}

	_, client, apps, err := openTestFlight(cmd.Context(), cfg, paths)
	if err != nil {
		tui.Err("connect to TestFlight: %v", err)
		return
	}

	app, err := findTestFlightApp(apps, appID)
	if err != nil {
		tui.Err("%v", err)
		return
	}

	versions, err := client.ListVersions(cmd.Context(), appID)
	if err != nil {
		tui.Err("list TestFlight versions: %v", err)
		return
	}

	if len(versions) == 0 {
		tui.Info("no TestFlight versions are available for %s", app.Name)
		return
	}

	tui.Heading("%s", app.Name)

	for _, version := range versions {
		tui.Item("%s", version.Name)
	}
}

func testFlightDecryptHandler(cmd *cobra.Command, args []string) {
	appID, err := parseTestFlightAppID(args[0])
	if err != nil {
		tui.Err("%v", err)
		return
	}

	cfg, paths, err := loadConfigOrDefault(rootDirOverride)
	if err != nil {
		tui.Err("%v", err)
		return
	}

	credentials := fairplay.Credentials{}

	credentials.Standard, err = loadCachedCredential(paths, cfg.Apple.DirectoryServicesIdentifier, false)
	if err != nil {
		tui.Err("no decryption credential for this account: %v", err)
		tui.Info("run `ipadecrypt bootstrap` to mint it")

		return
	}

	credentials.Arcade, err = loadCachedCredential(paths, cfg.Apple.DirectoryServicesIdentifier, true)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		tui.Err("Apple Arcade credential: %v", err)
		return
	}

	store, client, apps, err := openTestFlight(cmd.Context(), cfg, paths)
	if err != nil {
		tui.Err("connect to TestFlight: %v", err)
		return
	}

	app, err := findTestFlightApp(apps, appID)
	if err != nil {
		tui.Err("%v", err)
		return
	}

	deviceID := testFlightUDID
	if deviceID == "" {
		deviceID = cfg.TestFlight.DeviceID
	}

	if deviceID == "" {
		deviceID = cfg.Device.UDID
	}

	live := tui.NewLive()
	live.Spin("authorizing %s", app.Name)

	download, err := client.PrepareDownload(cmd.Context(), appID, testFlightVersion, deviceID)
	if err != nil {
		live.Fail("authorize TestFlight download: %v", err)
		return
	}

	live.OK("selected %s %s", app.Name, download.Version)

	encryptedPath, err := paths.CachedTestFlightIPA(download.BundleID, download.Version, download.BuildID)
	if err != nil {
		tui.Err("cache path: %v", err)
		return
	}

	if cachedTestFlightPackageMatches(encryptedPath, download.BundleID, download.Version) {
		tui.OK("using cached TestFlight IPA")
	} else {
		live = tui.NewLive()
		live.Spin("downloading TestFlight IPA")

		_, err = store.CompleteDownload(cfg.Apple.Account(), download.Ticket, encryptedPath, func(current, total int64) {
			live.Message("%s", transferProgressText("downloading TestFlight IPA", current, total))
			live.Progress(current, total)
		})
		if err != nil {
			_ = os.Remove(encryptedPath)
			_ = os.Remove(encryptedPath + ".tmp")

			live.Fail("TestFlight download failed: %v", err)

			return
		}

		if !cachedTestFlightPackageMatches(encryptedPath, download.BundleID, download.Version) {
			_ = os.Remove(encryptedPath)

			live.Fail("downloaded TestFlight IPA does not match %s %s", download.BundleID, download.Version)

			return
		}

		live.OK("downloaded %s %s", app.Name, download.Version)
	}

	outputPath, err := localOutputPath(decryptOutput, download.BundleID, download.Version)
	if err != nil {
		tui.Err("output path: %v", err)
		return
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		tui.Err("mkdir local: %v", err)
		return
	}

	if attemptOfflineDecrypt(encryptedPath, credentials, outputPath) == offlineNeedsDevice {
		tui.Err("this TestFlight build could not be fully decrypted offline")
	}
}

func parseTestFlightAppID(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)

	appID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || appID <= 0 {
		return 0, fmt.Errorf("invalid TestFlight app ID %q; run `ipadecrypt testflight apps`", raw)
	}

	return appID, nil
}

func findTestFlightApp(apps []testflight.App, appID int64) (testflight.App, error) {
	for _, app := range apps {
		if app.ID == appID {
			return app, nil
		}
	}

	return testflight.App{}, fmt.Errorf("app %d is not available in this TestFlight account", appID)
}

func cachedTestFlightPackageMatches(path, bundleID, version string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}

	cachedBundleID, cachedVersion, err := pipeline.AppInfo(path)

	return err == nil && cachedBundleID == bundleID && cachedVersion == version
}

func openTestFlight(ctx context.Context, cfg *config.Config, paths *config.Paths) (*appstore.Client, *testflight.Client, []testflight.App, error) {
	if cfg.Apple.DirectoryServicesIdentifier == "" {
		return nil, nil, nil, errors.New("no Apple account configured; run `ipadecrypt bootstrap` to sign in")
	}

	store, err := appstore.New(filepath.Join(paths.Root, "cookies"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open App Store session: %w", err)
	}

	newClient := func(session testflight.Session) (*testflight.Client, error) {
		return testflight.New(store, cfg.Apple.Account(), session)
	}
	session := testflight.Session{SessionID: cfg.TestFlight.SessionID, AccountID: cfg.TestFlight.AccountID}

	client, err := newClient(session)
	if err != nil {
		return nil, nil, nil, err
	}

	apps, err := client.ListApps(ctx)
	if !errors.Is(err, testflight.ErrSessionExpired) {
		if err == nil && testFlightUDID != "" && cfg.TestFlight.DeviceID != testFlightUDID {
			cfg.TestFlight.DeviceID = testFlightUDID
			if err := cfg.Save(); err != nil {
				return nil, nil, nil, fmt.Errorf("save TestFlight device: %w", err)
			}
		}

		return store, client, apps, err
	}

	deviceConfig, ok := configuredTestFlightDevice(cfg)
	if !ok {
		return nil, nil, nil, errors.New("no current TestFlight session; configure a jailbroken device or pass --udid")
	}

	tui.Info("finding the signed-in TestFlight session on the device…")

	session, err = testflight.ImportSession(ctx, deviceConfig)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("find TestFlight session: %w", err)
	}

	client, err = newClient(session)
	if err != nil {
		return nil, nil, nil, err
	}

	apps, err = client.ListApps(ctx)
	if err != nil {
		return store, client, nil, err
	}

	cfg.TestFlight = config.TestFlight{SessionID: session.SessionID, AccountID: session.AccountID, DeviceID: deviceConfig.UDID}
	if err := cfg.Save(); err != nil {
		return nil, nil, nil, fmt.Errorf("save TestFlight session: %w", err)
	}

	tui.OK("connected to TestFlight")

	return store, client, apps, nil
}

func testFlightImportHandler(cmd *cobra.Command, _ []string) {
	cfg, paths, err := loadConfigOrDefault(rootDirOverride)
	if err != nil {
		tui.Err("%v", err)
		return
	}

	deviceConfig, ok := configuredTestFlightDevice(cfg)
	if !ok {
		tui.Err("no jailbroken device configured")
		tui.Info("run `ipadecrypt bootstrap` or pass --udid for USB import")

		return
	}

	session, err := testflight.ImportSession(cmd.Context(), deviceConfig)
	if err != nil {
		tui.Err("import TestFlight session: %v", err)
		return
	}

	store, err := appstore.New(filepath.Join(paths.Root, "cookies"))
	if err != nil {
		tui.Err("open App Store session: %v", err)
		return
	}

	client, err := testflight.New(store, cfg.Apple.Account(), session)
	if err != nil {
		tui.Err("validate TestFlight session: %v", err)
		return
	}

	if _, err := client.ListApps(cmd.Context()); err != nil {
		tui.Err("validate TestFlight session: %v", err)
		return
	}

	cfg.TestFlight = config.TestFlight{
		SessionID: session.SessionID,
		AccountID: session.AccountID,
		DeviceID:  deviceConfig.UDID,
	}
	if err := cfg.Save(); err != nil {
		tui.Err("save TestFlight session: %v", err)
		return
	}

	tui.OK("imported TestFlight session")
	tui.Info("run `ipadecrypt testflight apps` to list available apps")
}

func configuredTestFlightDevice(cfg *config.Config) (config.Device, bool) {
	deviceConfig := cfg.Device
	if testFlightUDID != "" {
		deviceConfig.USB = true
		deviceConfig.UDID = testFlightUDID
		deviceConfig.Host = ""
		deviceConfig.Port = 2222
	}

	return deviceConfig, deviceConfig.USB || deviceConfig.Host != ""
}
