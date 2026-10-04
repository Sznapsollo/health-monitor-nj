package legacy_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Sznapsollo/health-monitor-nj/internal/adapter/legacy"
	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
)

func legacyMapping(t testing.TB) *legacy.Mapping {
	t.Helper()
	m, err := legacy.LoadMapping(filepath.Join("testdata", legacy.MappingFile))
	if err != nil || m == nil {
		t.Fatalf("mapping: %v, %v", m, err)
	}
	return m
}

func TestMappingReadsTheSendersPackets(t *testing.T) {
	mapped := legacy.NewWithOptions(legacy.Options{Mapping: legacyMapping(t)})
	cases := map[string]string{
		"request":       fixtureRequest,
		"customWarning": fixtureWarning,
		"serverStatus":  fixtureStatus,
		"sendLog":       fixtureSendLog,
	}
	for file, native := range cases {
		t.Run(file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", "legacy", file+".json"))
			if err != nil {
				t.Fatal(err)
			}
			got := adaptOne(t, mapped, string(raw))
			want := adaptOne(t, legacy.New(""), native)
			if got.Alert != nil {
				want.Alert.Category = strings.Replace(want.Alert.Category, "APP", "WEB", 1)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("mapped legacy packet differs from the native one\n got %+v\nwant %+v", got, want)
			}
		})
	}
}

func TestMappingRenamesKeysOnly(t *testing.T) {
	a := legacy.NewWithOptions(legacy.Options{Mapping: &legacy.Mapping{
		Types:  map[string]string{"oldWarning": legacy.TypeCustomWarning},
		Fields: map[string]string{"oldMessage": "message", "oldLevel": "level"},
	}})
	p := adaptOne(t, a, `{"type":"oldWarning", "oldMessage" : "oldLevel: \"oldMessage\":", "oldLevel":"ERROR",
	  "note":"oldMessage","start":"2026-09-19T10:11:12.123+0200"}`)
	if p.Alert == nil || p.Alert.Message != `oldLevel: "oldMessage":` || p.Alert.Level != protocol.LevelError {
		t.Fatalf("alert = %+v", p.Alert)
	}
	if p.Alert.Data["note"] != "oldMessage" {
		t.Errorf("data = %+v, want values untouched", p.Alert.Data)
	}
}

func TestOriginWords(t *testing.T) {
	a := legacy.NewWithOptions(legacy.Options{Mapping: &legacy.Mapping{
		Origins: map[string]string{"job": "JOBS", "default": "WEB"},
	}})
	for origin, want := range map[string]string{"job": "Error JOBS", "": "Error WEB", "cron": "Error WEB"} {
		p := adaptOne(t, a, `{"type":"customWarning","level":"ERROR","origin":"`+origin+`","start":"2026-09-19T10:11:12.123+0200"}`)
		if p.Alert.Category != want {
			t.Errorf("origin %q: category %q, want %q", origin, p.Alert.Category, want)
		}
	}
}

func TestMappingErrors(t *testing.T) {
	cases := map[string]string{
		"type":    "types: { x: nothingLikeIt }",
		"field":   "fields: { x: nothingLikeIt }",
		"unknown": "tpyes: { x: request }",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), legacy.MappingFile)
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := legacy.LoadMapping(path); err == nil {
				t.Error("accepted")
			}
		})
	}
	if m, err := legacy.LoadMapping(filepath.Join(t.TempDir(), "absent.yaml")); m != nil || err != nil {
		t.Errorf("absent file: %v, %v", m, err)
	}
}

func BenchmarkAdaptMappedRequest(b *testing.B) {
	a := legacy.NewWithOptions(legacy.Options{Mapping: legacyMapping(b)})
	raw, err := os.ReadFile(filepath.Join("testdata", "legacy", "request.json"))
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(raw)))
	for i := 0; i < b.N; i++ {
		if _, err := a.Adapt(raw); err != nil {
			b.Fatal(err)
		}
	}
}
