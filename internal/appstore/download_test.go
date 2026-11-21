package appstore

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"howett.net/plist"
)

func TestPublishPackageKeepsExistingIPAOnIdentityMismatch(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.ipa")
	output := filepath.Join(dir, "package.ipa")

	var archive bytes.Buffer

	writer := zip.NewWriter(&archive)

	entry, err := writer.Create("Payload/Example.app/Info.plist")
	if err != nil {
		t.Fatal(err)
	}

	info, err := plist.Marshal(map[string]string{
		"CFBundleIdentifier": "com.example.beta", "CFBundleShortVersionString": "1.0.0",
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

	if err := os.WriteFile(source, archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(output, archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	ticket := DownloadTicket{
		Transport: &PackageTransport{},
		Metadata: map[string]any{
			"bundleIdentifier": "com.example.beta", "bundleShortVersionString": "2.0.0",
		},
	}
	if err := publishPackage(source, output, ticket, &Account{}); err == nil {
		t.Fatal("published package with wrong version")
	}

	got, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(got, archive.Bytes()) {
		t.Fatalf("existing IPA was changed, read error: %v", err)
	}

	ticket.Metadata["bundleShortVersionString"] = "1.0.0"
	if err := publishPackage(source, output, ticket, &Account{}); err != nil {
		t.Fatalf("matching package failed validation: %v", err)
	}
}
