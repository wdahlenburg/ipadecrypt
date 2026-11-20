package testflight

import (
	"errors"
	"net/http"
	"regexp"
	"testing"
)

func TestSessionDigest(t *testing.T) {
	got := sessionDigest("session", "A72F1C5B-0F21-44C0-BD2A-23A61BA12D2E", "123456789")

	want := "8eb953ba1e3162c97032f862faa75f183b3e8525"
	if got != want {
		t.Fatalf("sessionDigest() = %q, want %q", got, want)
	}
}

func TestNewUUID(t *testing.T) {
	got, err := newUUID()
	if err != nil {
		t.Fatal(err)
	}

	if !regexp.MustCompile(`^[0-9A-F]{8}-[0-9A-F]{4}-4[0-9A-F]{3}-[89AB][0-9A-F]{3}-[0-9A-F]{12}$`).MatchString(got) {
		t.Fatalf("newUUID() = %q", got)
	}
}

func TestHTTPClientDoesNotForwardTestFlightHeadersThroughRedirects(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := newHTTPClient().CheckRedirect(request, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirect error = %v, want http.ErrUseLastResponse", err)
	}
}
