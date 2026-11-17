package main

import (
	"testing"
	"time"
)

func TestResolveMonth(t *testing.T) {
	now := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	got, err := resolveMonth("last", now)
	if err != nil || got.Format("2006-01") != "2024-12" { // wraps the year boundary
		t.Fatalf("%v %v", got, err)
	}
	got, err = resolveMonth("2025-03", now)
	if err != nil || got.Format("2006-01") != "2025-03" {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := resolveMonth("March", now); err == nil {
		t.Fatal("expected error")
	}
	if _, err := resolveMonth("", now); err == nil {
		t.Fatal("expected error for empty")
	}
}

func TestRunRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	var out, errw discard
	if code := run([]string{"reap"}, &out, &errw); code != 1 {
		t.Fatalf("code %d", code)
	}
	if code := run(nil, &out, &errw); code != 1 {
		t.Fatalf("code %d", code)
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
