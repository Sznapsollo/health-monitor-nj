# Go, as this codebase uses it

Aimed at someone fluent in Java or TypeScript. It covers only what appears in
`health-monitor-nj`, in the order you will meet it.

## Packages, not classes

A directory is a package; every file in it shares one namespace, so there are
no imports between files of the same package. Identifiers starting with a
capital letter are exported, lowercase ones are private to the package.
`internal/` is enforced by the compiler: nothing outside this module can
import it, which is why everything except `pkg/client` lives there.

```go
package config            // internal/config/config.go and config_test.go
func Load(path string)    // exported
func (c *Config) applyEnv // private, still callable from config_test.go
```

Tests in `package config` see private identifiers; tests in `package
config_test` (same directory, `_test` suffix) see only the public API and so
document it. Both styles appear here on purpose.

## Structs and methods, not inheritance

```go
type Server struct { cfg config.Intake; log *slog.Logger }

func (s *Server) Run(ctx context.Context) error   // pointer receiver: may mutate
func (c Config) Validate() error                  // value receiver: read-only copy
```

Embedding is composition, not inheritance: `protocol.Metric` embeds
`protocol.Header`, so `m.V` and `m.T` work and the JSON encoder flattens the
fields into one object.

## Interfaces are satisfied implicitly

No `implements`. A type that has the methods satisfies the interface. The
convention here: define an interface only where two
implementations exist. `intake.Handler` is a function type rather than a
one-method interface, which is idiomatic for callbacks.

## Errors are values

```go
if err := s.Run(ctx); err != nil {
    return fmt.Errorf("intake: %w", err)   // %w keeps the cause chain
}
```

`errors.Is(err, protocol.ErrLegacy)` tests the chain; `errors.As` extracts a
typed cause. There are no exceptions. `panic` is for programmer errors only,
and never crosses a package boundary here.

## Goroutines, channels, context

`go f()` starts a goroutine — a few KB of stack, not an OS thread. A
`context.Context` is how one is cancelled: it is always the first parameter,
and every loop that can block checks `<-ctx.Done()`.

```go
ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer cancel()
```

`sync.WaitGroup` waits for a group to finish; `chan T` passes values between
goroutines. `cmd/hm/main.go` uses both: three servers start, the first error
cancels the context, and the process shuts down in an orderly way.

Shared mutable state is guarded by a `sync.Mutex` (`intake.Server.bound`), and
`go test -race` in CI catches what is not.

## defer

`defer` runs a call when the function returns, whatever the exit path — the
idiomatic place to close what you opened.

```go
conn, err := net.Dial(...)
if err != nil { return err }
defer conn.Close()
```

## Slices and maps

`[]T` is a view over an array: passing one copies the header, not the
elements, so an append inside a function may or may not be visible outside —
return the slice. Maps are not safe for concurrent writes, which is why hot
state is owned by a single goroutine in the design.

## JSON

Struct tags drive encoding; unknown fields in the input are ignored, which is
what makes the protocol tolerant of senders that add their own keys.

```go
type Metric struct {
    Signal string            `json:"signal"`
    Dims   map[string]string `json:"dims,omitempty"`
}
```

`jsonschema:"…"` tags on the same structs feed `cmd/gen-schema`. A type with
`UnmarshalJSON` controls its own decoding — `protocol.Timestamp` does, so it
accepts every timestamp layout senders emit.

## Tests

Table-driven, no framework, no assertion library:

```go
tests := []struct{ name, input string }{...}
for _, tc := range tests {
    t.Run(tc.name, func(t *testing.T) { ... t.Fatalf("got %v, want %v", got, want) })
}
```

`t.Setenv` and `t.TempDir` clean up after themselves. Benchmarks live beside
the tests (`BenchmarkDecodeMetric`) and run with `go test -bench .`.

## Build and tooling

- `go build ./...` builds everything; `./...` means "this package and below".
- `gofmt` is not negotiable — there is one formatting, and CI checks it.
- `go vet` catches real mistakes; `golangci-lint` adds staticcheck and friends.
- `go:embed` compiles files into the binary (`web/embed.go` embeds the SPA).
- `CGO_ENABLED=0` keeps the binary static, which is why the SQLite driver is
  the pure-Go one.
- `go.mod` names the module and the minimum Go version; `go.sum` pins hashes.
  `GOTOOLCHAIN=auto` downloads the right compiler when the local one is older.

## Where to look first

`cmd/hm/main.go` reads top to bottom: load config, build the logger and
metrics, start the HTTP, admin and UDP servers, wait for a signal, shut down.
Everything else hangs off that.
