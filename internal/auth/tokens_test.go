package auth

import (
	"context"
	"testing"
	"time"
)

func TestDisplayTokenLastUsedIsWrittenSparingly(t *testing.T) {
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	m, err := Open(Options{Password: "pw", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	created, err := m.CreateDisplayToken(context.Background(), NewDisplayToken{Name: "tv"})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := m.VerifyDisplayToken(created.Secret); !ok {
		t.Fatal("a fresh token was refused")
	}
	first := m.DisplayTokens()[0].LastUsed
	if !first.Equal(now) {
		t.Fatalf("lastUsed = %v, want the first use recorded", first)
	}

	now = now.Add(20 * time.Second)
	if _, ok := m.VerifyDisplayToken(created.Secret); !ok {
		t.Fatal("token refused")
	}
	if got := m.DisplayTokens()[0].LastUsed; !got.Equal(first) {
		t.Errorf("lastUsed = %v, want it unchanged inside the minute", got)
	}

	now = now.Add(time.Minute)
	if _, ok := m.VerifyDisplayToken(created.Secret); !ok {
		t.Fatal("token refused")
	}
	if got := m.DisplayTokens()[0].LastUsed; !got.Equal(now) {
		t.Errorf("lastUsed = %v, want it moved on after a minute", got)
	}

	if _, ok := m.VerifyDisplayToken("not-a-token"); ok {
		t.Error("a made-up token was accepted")
	}
}
