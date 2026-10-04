package auth

import (
	"testing"
	"time"
)

func TestACookieExpiringTooFarAheadIsRefused(t *testing.T) {
	now := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	m, err := Open(Options{Password: "pw", TTL: time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Verify(m.sign(Session{Name: "anna", Kind: KindUser, Expires: now.Add(time.Hour)})); !ok {
		t.Fatal("a cookie with a normal expiry was refused")
	}
	if _, ok := m.Verify(m.sign(Session{Name: "anna", Kind: KindUser, Expires: now.Add(365 * 24 * time.Hour)})); ok {
		t.Fatal("a cookie expiring a year from now was accepted")
	}
}

func TestSessionsAreSignedWithThePassword(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	if string(signingKey(secret, "one")) == string(signingKey(secret, "two")) {
		t.Fatal("two passwords gave the same signing key")
	}
}
