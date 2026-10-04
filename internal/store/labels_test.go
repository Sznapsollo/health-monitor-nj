package store_test

import (
	"context"
	"testing"

	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

var labelled = store.Layout{
	Dims:   []string{"account", "accountName"},
	Labels: map[string]string{"account": "accountName"},
}

func names(t *testing.T, s *store.Store) map[string]string {
	t.Helper()
	labels, err := s.Labels(context.Background(), "test", "requests")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, l := range labels {
		if l.Dim != "account" {
			t.Errorf("label %+v is for the wrong dimension", l)
		}
		out[l.Value] = l.Name
	}
	return out
}

func TestNamesAreStoredAndFollowRenames(t *testing.T) {
	s := open(t)
	minute := state.MinuteOf(base)
	writeWith(t, s, labelled,
		sample(minute, map[string]string{"account": "42", "accountName": "Acme"}, 1, 10),
		sample(minute, map[string]string{"account": "7"}, 1, 10),
		sample(minute, map[string]string{"account": "9", "accountName": "9"}, 1, 10),
	)
	if got := names(t, s); len(got) != 1 || got["42"] != "Acme" {
		t.Fatalf("names = %v, want only 42 named Acme", got)
	}

	writeWith(t, s, labelled,
		sample(minute+1, map[string]string{"account": "42", "accountName": "Acme Ltd"}, 1, 10))
	writeWith(t, s, labelled,
		sample(minute-5, map[string]string{"account": "42", "accountName": "Acme"}, 1, 10))
	if got := names(t, s); got["42"] != "Acme Ltd" {
		t.Errorf("names = %v, want the newest name kept over a late older one", got)
	}
}

func TestNamesArePurgedWithTheSignal(t *testing.T) {
	s := open(t)
	minute := state.MinuteOf(base)
	writeWith(t, s, labelled,
		sample(minute-100, map[string]string{"account": "1", "accountName": "Old"}, 1, 10),
		sample(minute, map[string]string{"account": "2", "accountName": "New"}, 1, 10),
	)
	if _, err := s.Purge(context.Background(), "test", "requests", minute-50); err != nil {
		t.Fatal(err)
	}
	if got := names(t, s); len(got) != 1 || got["2"] != "New" {
		t.Errorf("names = %v, want only the recently seen one", got)
	}
}
