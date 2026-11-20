package testflight

import (
	"testing"

	"github.com/londek/ipadecrypt/internal/appstore"
	"howett.net/plist"
)

func TestParseInstallData(t *testing.T) {
	sinf, err := plist.Marshal(sinfEnvelope{Sinfs: []appstore.Sinf{{ID: 7, Data: []byte{1, 2, 3}}}}, plist.XMLFormat)
	if err != nil {
		t.Fatal(err)
	}

	ticket, identity, err := parseInstallData(installData{
		ITunesMetadata: map[string]any{"betaExternalVersionId": int64(123)},
		Framework: framework{
			BundleID:     "com.example.beta",
			Version:      "2.3.0",
			BuildVersion: "9",
		},
		Packages: packages{Full: fullPackage{
			URL:         "https://iosapps.itunes.apple.com/path/app.ipa?token=secret",
			Sinf:        string(sinf),
			PackageSize: 456,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if identity.BundleID != "com.example.beta" || ticket.Version() != "2.3.0" || ticket.FileSize() != 456 {
		t.Fatalf("identity = %#v, version = %q, size = %d", identity, ticket.Version(), ticket.FileSize())
	}

	if len(ticket.Sinfs) != 1 || ticket.Sinfs[0].ID != 7 {
		t.Fatalf("sinfs = %#v", ticket.Sinfs)
	}
}

func TestParseInstallDataRejectsUntrustedPackageURL(t *testing.T) {
	_, _, err := parseInstallData(installData{
		URL: "https://example.com/app.ipa?token=secret",
	})
	if err == nil {
		t.Fatal("accepted an untrusted package URL")
	}
}
