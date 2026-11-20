package testflight

import (
	"encoding/hex"
	"strings"
	"testing"

	"howett.net/plist"
)

func TestSessionFromCachedRequest(t *testing.T) {
	array := make([]any, 27)
	for index := range array {
		array[index] = ""
	}

	array[3] = map[string]any{"x-session-id": "test-session"}

	archive, err := plist.Marshal(map[string]any{"Array": array, "Version": uint64(1)}, plist.BinaryFormat)
	if err != nil {
		t.Fatal(err)
	}

	session, err := sessionFromCachedRequest("https://testflight.apple.com/v2/accounts/account-id/apps/123/builds?platform=ios", archive)
	if err != nil {
		t.Fatal(err)
	}

	if session.SessionID != "test-session" || session.AccountID != "account-id" {
		t.Fatalf("session = %#v", session)
	}
}

func TestSessionFromCachedRequestRejectsConflictingSessions(t *testing.T) {
	array := []any{
		map[string]any{"X-Session-Id": "first"},
		map[string]any{"X-Session-Id": "second"},
	}

	archive, err := plist.Marshal(map[string]any{"Array": array}, plist.BinaryFormat)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := sessionFromCachedRequest("https://testflight.apple.com/v4/accounts/account-id/apps", archive); err == nil {
		t.Fatal("accepted conflicting TestFlight sessions")
	}
}

func TestSessionFromCachedRequestRejectsInvalidSessionHeader(t *testing.T) {
	archive, err := plist.Marshal(map[string]any{
		"Array": []any{map[string]any{"X-Session-Id": "session\tvalue"}},
	}, plist.BinaryFormat)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := sessionFromCachedRequest("https://testflight.apple.com/v4/accounts/account-id/apps", archive); err == nil {
		t.Fatal("accepted a session containing header whitespace")
	}
}

func TestSessionFromCachedRequestRejectsOtherOrigin(t *testing.T) {
	if _, err := sessionFromCachedRequest("https://example.com/v4/accounts/account-id/apps", nil); err == nil {
		t.Fatal("accepted a non-TestFlight origin")
	}
}

func TestSessionFromCacheRow(t *testing.T) {
	archive, err := plist.Marshal(map[string]any{
		"Array": []any{map[string]any{"X-Session-Id": "cached-session"}},
	}, plist.BinaryFormat)
	if err != nil {
		t.Fatal(err)
	}

	row := "https://testflight.apple.com/v3/accounts/account-id/apps\t" + strings.ToUpper(hex.EncodeToString(archive))

	session, err := sessionFromCacheRow(row)
	if err != nil {
		t.Fatal(err)
	}

	if session.SessionID != "cached-session" || session.AccountID != "account-id" {
		t.Fatalf("session = %#v", session)
	}
}
