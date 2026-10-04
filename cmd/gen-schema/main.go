// Command gen-schema writes the JSON Schema for wire protocol v1 from the Go
// structs in internal/protocol. The output is committed; CI fails when it is
// stale. Run it with `make gen`, which also regenerates web/src/protocol.ts.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/Sznapsollo/health-monitor-nj/internal/protocol"
	"github.com/invopop/jsonschema"
)

func main() {
	out := flag.String("out", "schema/hm-protocol-v1.json", "file to write")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "gen-schema:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	r := &jsonschema.Reflector{
		ExpandedStruct:             true,
		RequiredFromJSONSchemaTags: false,
		DoNotReference:             false,
		// The server ignores unknown fields, so the published schema must not
		// reject senders that add their own.
		AllowAdditionalProperties: true,
	}
	// Timestamps are a Go struct but a string on the wire.
	r.Mapper = func(t reflect.Type) *jsonschema.Schema {
		if t == reflect.TypeOf(protocol.Timestamp{}) {
			return &jsonschema.Schema{Type: "string", Format: "date-time"}
		}
		return nil
	}
	if err := r.AddGoComments("github.com/Sznapsollo/health-monitor-nj", "./internal/protocol"); err != nil {
		return fmt.Errorf("read doc comments: %w", err)
	}

	root := &jsonschema.Schema{
		Version:     jsonschema.Version,
		ID:          "https://github.com/Sznapsollo/health-monitor-nj/schema/hm-protocol-v1.json",
		Title:       "healthMonitorNJ wire protocol v1",
		Description: "One JSON object per UDP datagram, or an array of them for batching.",
		Definitions: jsonschema.Definitions{},
	}
	for _, e := range []struct {
		name string
		t    protocol.Type
		v    any
	}{
		{"Metric", protocol.TypeMetric, protocol.Metric{}},
		{"Gauge", protocol.TypeGauge, protocol.Gauge{}},
		{"Status", protocol.TypeStatus, protocol.Status{}},
		{"Alert", protocol.TypeAlert, protocol.Alert{}},
		{"Log", protocol.TypeLog, protocol.Log{}},
		{"Chunk", protocol.TypeChunk, protocol.Chunk{}},
	} {
		s := r.Reflect(e.v)
		// Fold the reflected definitions into one document.
		for name, def := range s.Definitions {
			root.Definitions[name] = def
		}
		s.Definitions = nil
		s.Version = ""
		s.ID = ""
		// The Go structs share one Header, so narrow the discriminator per
		// envelope; that is what makes the generated TypeScript a proper
		// discriminated union.
		if prop, ok := s.Properties.Get("t"); ok {
			prop.Enum = []any{string(e.t)}
		}
		root.Definitions[e.name] = s
		root.OneOf = append(root.OneOf, &jsonschema.Schema{Ref: "#/$defs/" + e.name})
	}

	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	return os.WriteFile(out, b, 0o644)
}
