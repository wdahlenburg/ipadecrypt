package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"howett.net/plist"
)

func TestParseTestFlightAppID(t *testing.T) {
	got, err := parseTestFlightAppID(" 1095418609 ")
	if err != nil || got != 1095418609 {
		t.Fatalf("app ID = %d, %v", got, err)
	}

	for _, input := range []string{"", "Ford", "-1", "0"} {
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
