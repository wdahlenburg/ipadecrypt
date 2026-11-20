package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/londek/ipadecrypt/internal/testflight"
	"github.com/londek/ipadecrypt/internal/tui"
	"howett.net/plist"
)

func TestParseTestFlightAppID(t *testing.T) {
	got, err := parseTestFlightAppID(" 1000000000 ")
	if err != nil || got != 1000000000 {
		t.Fatalf("app ID = %d, %v", got, err)
	}

	for _, input := range []string{"", "App", "-1", "0"} {
		if _, err := parseTestFlightAppID(input); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
}

func TestCachedTestFlightPackageMatchesIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "package.ipa")

	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	writer := zip.NewWriter(file)

	entry, err := writer.Create("Payload/Example.app/Info.plist")
	if err != nil {
		t.Fatal(err)
	}

	info, err := plist.Marshal(map[string]any{
		"CFBundleIdentifier":         "com.example.beta",
		"CFBundleShortVersionString": "2.3.0",
	}, plist.XMLFormat)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := entry.Write(info); err != nil {
		t.Fatal(err)
	}

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if !cachedTestFlightPackageMatches(path, "com.example.beta", "2.3.0") {
		t.Fatal("matching TestFlight package was rejected")
	}

	if cachedTestFlightPackageMatches(path, "com.example.other", "2.3.0") {
		t.Fatal("mismatched TestFlight bundle was accepted")
	}
}

func TestRenderTestFlightLinkMatchesAppList(t *testing.T) {
	previous := tui.Out
	defer func() { tui.Out = previous }()

	var output bytes.Buffer

	tui.Out = &output

	renderTestFlightLink(testflight.PublicLink{
		Status: testflight.PublicLinkOpen,
		App: testflight.PublicLinkApp{
			ID:           1_000_000_000,
			Name:         "Example App",
			ProviderName: "Example Developer",
		},
	})

	want := "  Example Developer\n    • Example App (1000000000)\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}
