package signal_test

import (
	"sync"
	"testing"

	"github.com/Sznapsollo/health-monitor-nj/internal/signal"
)

func def(platform, name string, dims ...string) *signal.Definition {
	d := &signal.Definition{
		Platform: platform,
		Name:     name,
		Kind:     signal.KindTimeseries,
		Dims:     dims,
	}
	// Add exercises the same defaulting path the catalogue uses.
	return d
}

func TestRegistryLookup(t *testing.T) {
	r := signal.NewRegistry()
	if err := r.Add(def("example", "requests", "url")); err != nil {
		t.Fatalf("add: %v", err)
	}

	if _, ok := r.Lookup("example", "requests"); !ok {
		t.Error("the signal that was just added is not found")
	}
	if _, ok := r.Lookup("example", "nope"); ok {
		t.Error("an unknown signal was found")
	}
	if _, ok := r.Lookup("other", "requests"); ok {
		t.Error("signals leaked across platforms")
	}
}

func TestRegistryRejectsInvalid(t *testing.T) {
	r := signal.NewRegistry()
	if err := r.Add(&signal.Definition{Platform: "p", Name: "x", Kind: "sparkle"}); err == nil {
		t.Error("want an error for an unknown kind")
	}
	if err := r.Add(&signal.Definition{Name: "x", Kind: signal.KindTimeseries}); err == nil {
		t.Error("want an error for a definition without a platform")
	}
}

func TestAutoRegister(t *testing.T) {
	r := signal.NewRegistry()

	// Off by default: an unknown signal stays unknown, so the caller
	// quarantines it instead of inventing state.
	if _, ok := r.LookupOrRegister("p", "queueDepth", signal.KindGauge, nil); ok {
		t.Fatal("auto-registered while the platform had it switched off")
	}

	var got []*signal.Definition
	r.OnRegister(func(d *signal.Definition) { got = append(got, d) })
	r.SetAutoRegister("p", true)

	d, ok := r.LookupOrRegister("p", "newMetric", signal.KindTimeseries, []string{"url", "account"})
	if !ok {
		t.Fatal("auto-registration did not happen")
	}
	if !d.AutoRegistered {
		t.Error("the definition is not marked as auto-registered")
	}
	if d.Retention.HotDetailMinutes != signal.DefaultHotDetailMinutes {
		t.Errorf("retention = %+v, want the defaults", d.Retention)
	}
	if len(got) != 1 || got[0].Name != "newMetric" {
		t.Errorf("callback fired %d times: %+v", len(got), got)
	}

	// A second packet finds the same definition and does not fire again.
	if _, ok := r.LookupOrRegister("p", "newMetric", signal.KindTimeseries, nil); !ok {
		t.Fatal("the auto-registered signal is not found the second time")
	}
	if len(got) != 1 {
		t.Errorf("callback fired %d times, want once", len(got))
	}

	// Kinds that need configuration are never invented.
	if _, ok := r.LookupOrRegister("p", "someLog", signal.KindLog, nil); ok {
		t.Error("a log signal was auto-registered")
	}
}

func TestReplacePlatformKeepsAutoRegistered(t *testing.T) {
	r := signal.NewRegistry()
	r.SetAutoRegister("p", true)
	if err := r.Add(def("p", "requests", "url")); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.LookupOrRegister("p", "invented", signal.KindTimeseries, []string{"x"}); !ok {
		t.Fatal("auto-registration failed")
	}

	// A catalogue reload that no longer mentions `requests` drops it, but the
	// invented signal survives because no file owns it.
	c := &signal.Catalogue{Platform: "p", Signals: map[string]*signal.Definition{
		"jobs": def("p", "jobs", "jobName"),
	}}
	if err := r.ReplacePlatform(c); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if _, ok := r.Lookup("p", "requests"); ok {
		t.Error("a signal removed from the catalogue is still registered")
	}
	if _, ok := r.Lookup("p", "jobs"); !ok {
		t.Error("the new signal is missing")
	}
	if _, ok := r.Lookup("p", "invented"); !ok {
		t.Error("the auto-registered signal did not survive the reload")
	}
}

func TestRegistryListing(t *testing.T) {
	r := signal.NewRegistry()
	for _, d := range []*signal.Definition{
		def("b", "two"), def("a", "zebra"), def("a", "alpha"),
	} {
		if err := r.Add(d); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.Platforms(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("platforms = %v, want them sorted", got)
	}
	names := []string{}
	for _, d := range r.All() {
		names = append(names, d.Platform+"/"+d.Name)
	}
	want := []string{"a/alpha", "a/zebra", "b/two"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("all = %v, want %v", names, want)
		}
	}
}

func TestRegistryConcurrentUse(t *testing.T) {
	r := signal.NewRegistry()
	r.SetAutoRegister("p", true)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				r.LookupOrRegister("p", "shared", signal.KindTimeseries, []string{"url"})
				r.Lookup("p", "shared")
				r.Definitions("p")
			}
		}()
	}
	wg.Wait()
	if defs := r.Definitions("p"); len(defs) != 1 {
		t.Fatalf("definitions = %d, want exactly one", len(defs))
	}
}
