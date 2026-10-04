// Package obs holds the Prometheus collectors and the logger factory. It is
// the one place allowed to keep registered collectors, because Prometheus
// requires a single registration per process.
package obs

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics are the collectors exposed at /api/metrics.
type Metrics struct {
	PacketsReceived *prometheus.CounterVec
	PacketsRejected *prometheus.CounterVec
	BytesReceived   prometheus.Counter
	ReadErrors      prometheus.Counter
	BuildInfo       *prometheus.GaugeVec
}

// NewMetrics registers the collectors on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	f := promauto.With(reg)
	return &Metrics{
		PacketsReceived: f.NewCounterVec(prometheus.CounterOpts{
			Name: "hm_intake_packets_received_total",
			Help: "Datagrams accepted by the intake, by envelope type.",
		}, []string{"type"}),
		PacketsRejected: f.NewCounterVec(prometheus.CounterOpts{
			Name: "hm_intake_packets_rejected_total",
			Help: "Datagrams the intake could not use, by reason.",
		}, []string{"reason"}),
		BytesReceived: f.NewCounter(prometheus.CounterOpts{
			Name: "hm_intake_bytes_received_total",
			Help: "Bytes read from the UDP sockets.",
		}),
		ReadErrors: f.NewCounter(prometheus.CounterOpts{
			Name: "hm_intake_read_errors_total",
			Help: "Errors returned by the UDP read loop.",
		}),
		BuildInfo: f.NewGaugeVec(prometheus.GaugeOpts{
			Name: "hm_build_info",
			Help: "Build metadata; always 1.",
		}, []string{"version", "commit", "go_version"}),
	}
}

// NewLogger builds the process logger from the configured level and format.
func NewLogger(level, format string) (*slog.Logger, error) {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		return nil, fmt.Errorf("obs: unknown log level %q", level)
	}
	opts := &slog.HandlerOptions{Level: lvl}
	if strings.ToLower(format) == "text" {
		return slog.New(slog.NewTextHandler(os.Stdout, opts)), nil
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts)), nil
}
