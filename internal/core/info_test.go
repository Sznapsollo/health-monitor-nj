package core_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/Sznapsollo/health-monitor-nj/internal/core"
	"github.com/Sznapsollo/health-monitor-nj/internal/info"
	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
	"github.com/Sznapsollo/health-monitor-nj/internal/state"
	"github.com/Sznapsollo/health-monitor-nj/internal/status"
	"github.com/Sznapsollo/health-monitor-nj/internal/store"
)

func TestAnInfoReportIsKeptWholeAndCountsAsAlive(t *testing.T) {
	c := &clock{t: base}
	reg := signal.NewRegistry()
	if err := reg.Add(&signal.Definition{
		Platform: "test", Name: "jobsList", Kind: signal.KindInfo, PacketType: "configServerJobsListStatus",
		Retention: signal.Retention{Versions: 2},
	}); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	statuses, err := status.Open(db.DB(), c.now)
	if err != nil {
		t.Fatal(err)
	}
	reports, err := info.Open(context.Background(), db.DB())
	if err != nil {
		t.Fatal(err)
	}
	brain := core.New(state.NewStore(reg, c.now), reg, slog.New(slog.NewTextHandler(discard{}, nil)), c.now)
	brain.SetAlerting(core.Alerting{Statuses: statuses, Info: reports})

	report := func(n int) protocol.Packet {
		return protocol.Packet{
			Header: protocol.Header{V: 1, T: protocol.TypeStatus, Platform: "test"},
			Status: &protocol.Status{Signal: "jobsList", Key: "config-1", Payload: map[string]any{"kind": "info"},
				Content: map[string]any{"jobsStatusMap": map[string]any{"saveDocument": map[string]any{"heartBeat": n}}}},
		}
	}
	for n := range 3 {
		brain.Packets([]protocol.Packet{report(n)})
	}

	list := statuses.List("test")
	if len(list) != 1 || list[0].Key != "config-1" || list[0].Offline {
		t.Fatalf("status = %+v, want config-1 alive", list)
	}
	versions, err := reports.Versions(context.Background(), "test", "jobsList", "config-1")
	if err != nil || len(versions) != 2 {
		t.Fatalf("versions = %v (%v), want the 2 the signal keeps", versions, err)
	}
	_, content, _ := reports.Content(context.Background(), "test", "jobsList", "config-1", 0)
	var got struct {
		Jobs map[string]struct{ HeartBeat int } `json:"jobsStatusMap"`
	}
	_ = json.Unmarshal(content, &got)
	if got.Jobs["saveDocument"].HeartBeat != 2 {
		t.Fatalf("latest = %s, want the whole last report", content)
	}
}

func TestAnInfoSignalOffStatusKeepsReportsWithoutARow(t *testing.T) {
	c := &clock{t: base}
	reg := signal.NewRegistry()
	if err := reg.Add(&signal.Definition{
		Platform: "test", Name: "dailyReport", Kind: signal.KindInfo, PacketType: "godzinkiDailyReportStatus",
		Retention: signal.Retention{Versions: 20}, NoStatus: true,
	}); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	statuses, err := status.Open(db.DB(), c.now)
	if err != nil {
		t.Fatal(err)
	}
	reports, err := info.Open(context.Background(), db.DB())
	if err != nil {
		t.Fatal(err)
	}
	brain := core.New(state.NewStore(reg, c.now), reg, slog.New(slog.NewTextHandler(discard{}, nil)), c.now)
	brain.SetAlerting(core.Alerting{Statuses: statuses, Info: reports})

	brain.Packets([]protocol.Packet{{
		Header: protocol.Header{V: 1, T: protocol.TypeStatus, Platform: "test"},
		Status: &protocol.Status{Signal: "dailyReport", Key: "app-1", Payload: map[string]any{"kind": "info"},
			Content: map[string]any{"message": "done"}},
	}})

	if list := statuses.List("test"); len(list) != 0 {
		t.Fatalf("status = %+v, want no row", list)
	}
	versions, err := reports.Versions(context.Background(), "test", "dailyReport", "app-1")
	if err != nil || len(versions) != 1 {
		t.Fatalf("versions = %v (%v), want the report kept", versions, err)
	}
}
