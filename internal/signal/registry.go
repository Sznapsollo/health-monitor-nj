package signal

import (
	"fmt"
	"sort"
	"sync"
)

// Registry answers "what is (platform, signal)?" for the whole server. It is
// read on every packet and replaced wholesale when a catalogue file changes,
// so reads take a read lock and never block each other.
type Registry struct {
	mu sync.RWMutex
	// defs is keyed by platform then signal name.
	defs map[string]map[string]*Definition
	// autoRegister lets a platform accept signals its catalogue does not
	// declare, so a new chart appears without a server-side change.
	autoRegister map[string]bool
	// onRegister is called when a signal is auto-registered, so the rest of
	// the server can build state for it.
	onRegister func(*Definition)
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		defs:         make(map[string]map[string]*Definition),
		autoRegister: make(map[string]bool),
	}
}

// OnRegister sets the callback fired when a definition is added after start.
func (r *Registry) OnRegister(fn func(*Definition)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onRegister = fn
}

// SetAutoRegister allows (or forbids) inventing signals for a platform.
func (r *Registry) SetAutoRegister(platform string, allow bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.autoRegister[platform] = allow
}

// Add installs one definition, replacing any signal of the same name. Missing
// retention, caps and display names are filled in here, so a definition built
// in code behaves exactly like one loaded from a catalogue.
func (r *Registry) Add(d *Definition) error {
	d.applyDefaults()
	if err := d.Validate(); err != nil {
		return err
	}
	if d.Platform == "" {
		return fmt.Errorf("signal %q: no platform", d.Name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addLocked(d)
	return nil
}

func (r *Registry) addLocked(d *Definition) {
	byName, ok := r.defs[d.Platform]
	if !ok {
		byName = make(map[string]*Definition)
		r.defs[d.Platform] = byName
	}
	byName[d.Name] = d
}

// AddCatalogue installs every signal of one platform.
func (r *Registry) AddCatalogue(c *Catalogue) error {
	for _, d := range c.Signals {
		if err := r.Add(d); err != nil {
			return err
		}
	}
	return nil
}

// Lookup returns the definition for a packet, inventing one when the platform
// allows it and the kind can be served with defaults. The second result says
// whether the definition is known; an unknown signal is quarantined by the
// caller, never dropped silently.
func (r *Registry) Lookup(platform, name string) (*Definition, bool) {
	r.mu.RLock()
	d, ok := r.defs[platform][name]
	r.mu.RUnlock()
	if ok {
		return d, true
	}
	return nil, false
}

// LookupOrRegister is Lookup plus auto-registration: when the platform has
// autoRegister on and the kind is one that works without configuration, a
// definition with default retention and the packet's own dimensions is
// created and kept.
func (r *Registry) LookupOrRegister(platform, name string, kind Kind, dims []string) (*Definition, bool) {
	if d, ok := r.Lookup(platform, name); ok {
		return d, true
	}
	if kind != KindTimeseries && kind != KindGauge {
		return nil, false
	}
	r.mu.Lock()
	// Another goroutine may have registered it while the write lock was
	// being acquired.
	if d, ok := r.defs[platform][name]; ok {
		r.mu.Unlock()
		return d, true
	}
	if !r.autoRegister[platform] {
		r.mu.Unlock()
		return nil, false
	}
	d := &Definition{
		Platform:       platform,
		Name:           name,
		Kind:           kind,
		Dims:           append([]string(nil), dims...),
		AutoRegistered: true,
	}
	sort.Strings(d.Dims)
	d.applyDefaults()
	if err := d.Validate(); err != nil {
		r.mu.Unlock()
		return nil, false
	}
	r.addLocked(d)
	fn := r.onRegister
	r.mu.Unlock()
	if fn != nil {
		fn(d)
	}
	return d, true
}

// Packets is the platform's built-in packets signal, added the first time it
// is asked for; nil for a platform nothing else declares, so a packet naming
// a made-up platform does not bring it into being.
func (r *Registry) Packets(platform string) *Definition {
	if d, ok := r.Lookup(platform, PacketsSignal); ok {
		return d
	}
	r.mu.Lock()
	byName, known := r.defs[platform]
	if !known {
		r.mu.Unlock()
		return nil
	}
	if d, ok := byName[PacketsSignal]; ok {
		r.mu.Unlock()
		return d
	}
	d := PacketsDefinition(platform)
	r.addLocked(d)
	fn := r.onRegister
	r.mu.Unlock()
	if fn != nil {
		fn(d)
	}
	return d
}

// Platforms lists the known platforms, sorted.
func (r *Registry) Platforms() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.defs))
	for p := range r.defs {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Definitions lists one platform's signals, sorted by name.
func (r *Registry) Definitions(platform string) []*Definition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	byName := r.defs[platform]
	out := make([]*Definition, 0, len(byName))
	for _, d := range byName {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// InfoFor names the info signal that keeps packets of one of these types,
// empty when none does.
func (r *Registry) InfoFor(platform string, packetTypes ...string) string {
	return r.forPacketType(KindInfo, platform, packetTypes)
}

// MetricFor names the timeseries charted from packets of one of these types,
// empty when none is.
func (r *Registry) MetricFor(platform string, packetTypes ...string) string {
	return r.forPacketType(KindTimeseries, platform, packetTypes)
}

func (r *Registry) forPacketType(kind Kind, platform string, packetTypes []string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, d := range r.defs[platform] {
		if d.Kind != kind {
			continue
		}
		for _, t := range packetTypes {
			if t != "" && t == d.PacketType {
				return d.Name
			}
		}
	}
	return ""
}

// All lists every definition of every platform, sorted by platform then name.
func (r *Registry) All() []*Definition {
	var out []*Definition
	for _, p := range r.Platforms() {
		out = append(out, r.Definitions(p)...)
	}
	return out
}

// ReplacePlatform swaps every signal of one platform, which is what a
// catalogue file change does. Signals that disappear from the file are
// removed; their state is dropped by the caller.
func (r *Registry) ReplacePlatform(c *Catalogue) error {
	for _, d := range c.Signals {
		if err := d.Validate(); err != nil {
			return err
		}
	}
	byName := make(map[string]*Definition, len(c.Signals))
	for name, d := range c.Signals {
		byName[name] = d
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Auto-registered and built-in signals are not in the file but must
	// survive a reload.
	for name, d := range r.defs[c.Platform] {
		if d.AutoRegistered || d.BuiltIn {
			if _, replaced := byName[name]; !replaced {
				byName[name] = d
			}
		}
	}
	if len(byName) == 0 {
		delete(r.defs, c.Platform)
		return nil
	}
	r.defs[c.Platform] = byName
	return nil
}
