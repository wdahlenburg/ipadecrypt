package testflight

import (
	"context"
	"database/sql"
	"path/filepath"
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

func TestReadCacheSnapshot(t *testing.T) {
	archive, err := plist.Marshal(map[string]any{
		"Array": []any{map[string]any{"X-Session-Id": "cached-session"}},
	}, plist.BinaryFormat)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "Cache.db")

	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}

	for _, statement := range []string{
		`CREATE TABLE cfurl_cache_response (entry_ID INTEGER PRIMARY KEY, request_key TEXT NOT NULL)`,
		`CREATE TABLE cfurl_cache_blob_data (entry_ID INTEGER PRIMARY KEY, request_object BLOB NOT NULL)`,
		`INSERT INTO cfurl_cache_response(entry_ID, request_key) VALUES (1, 'https://testflight.apple.com/v3/accounts/account-id/apps')`,
	} {
		if _, err := database.Exec(statement); err != nil {
			database.Close()
			t.Fatal(err)
		}
	}

	if _, err := database.Exec(`INSERT INTO cfurl_cache_blob_data(entry_ID, request_object) VALUES (1, ?)`, archive); err != nil {
		database.Close()
		t.Fatal(err)
	}

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	requests, err := readCacheSnapshot(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}

	session, err := sessionFromCachedRequest(requests[0].url, requests[0].archive)
	if err != nil {
		t.Fatal(err)
	}

	if session.SessionID != "cached-session" || session.AccountID != "account-id" {
		t.Fatalf("session = %#v", session)
	}
}

func TestReadOnlySQLiteDSN(t *testing.T) {
	got := readOnlySQLiteDSN(filepath.Join(string(filepath.Separator), "tmp", "Cache db"))
	if got != "file:///tmp/Cache%20db?mode=ro" {
		t.Fatalf("DSN = %q", got)
	}
}
