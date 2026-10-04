package api_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sznapsollo/health-monitor-nj/internal/api"
	"github.com/Sznapsollo/health-monitor-nj/internal/intake"
	"github.com/prometheus/client_golang/prometheus"
)

func TestPacketsAnswerEmptyUntilSomethingArrives(t *testing.T) {
	for _, tc := range []struct {
		tap  *intake.Tap
		want int
	}{{nil, http.StatusServiceUnavailable}, {&intake.Tap{}, http.StatusOK}} {
		h := api.Handler(api.Deps{
			Log: slog.New(slog.NewTextHandler(discard{}, nil)), Metrics: prometheus.NewRegistry(), Tap: tc.tap,
		})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/packets?since=0", nil))
		if rec.Code != tc.want {
			t.Fatalf("tap %v: %d, want %d", tc.tap != nil, rec.Code, tc.want)
		}
		if tc.tap == nil {
			continue
		}
		var body struct {
			Entries []intake.TapEntry `json:"entries"`
			Seq     int64             `json:"seq"`
			Keep    int               `json:"keep"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Entries == nil || len(body.Entries) != 0 || body.Seq != 0 || body.Keep != intake.TapSize {
			t.Fatalf("body = %+v", body)
		}
	}
}
