package config

import (
	"path/filepath"
	"testing"
)

func TestCachedTestFlightIPA(t *testing.T) {
	paths, err := NewPaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	got, err := paths.CachedTestFlightIPA("com.example.beta", "2.3.0", 123456)
	if err != nil {
		t.Fatal(err)
	}

	wantSuffix := filepath.Join("cache", "testflight", "com.example.beta", "2.3.0", "123456", "package.ipa")
	if filepath.Clean(got) != filepath.Join(paths.Root, wantSuffix) {
		t.Fatalf("path = %q", got)
	}

	for _, test := range []struct {
		bundle  string
		version string
		build   int64
	}{
		{bundle: "../escape", version: "2.3.0", build: 1},
		{bundle: "com.example.beta", version: "../../escape", build: 1},
		{bundle: "com.example.beta", version: "2.3.0", build: 0},
	} {
		if _, err := paths.CachedTestFlightIPA(test.bundle, test.version, test.build); err == nil {
			t.Fatalf("accepted cache identity %#v", test)
		}
	}
}
