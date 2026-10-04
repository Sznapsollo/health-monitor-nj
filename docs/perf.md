# Measured performance

Numbers from `cmd/loadgen` against a real server, not estimates. Re-run them
with `make load` after any change to the ingest path.

## The gates

| | Target | Measured | |
|---|---|---|---|
| Production volume (v1) | 1 000 datagrams/s, zero loss | **998/s, 0 lost** | pass |
| Production volume (legacy v0) | 1 000 datagrams/s, zero loss | **998/s, 0 lost** | pass |
| product sustained | 200 000 datagrams/s, zero loss | **199 962/s, 0 lost** | pass |
| cold start | < 500 ms including restore | **< 1 ms** for 38 rows | pass |
| image | < 30 MB | **12.2 MB** distroless | pass |
| RSS | < 100 MB at default retention | **109 MB** at 200 k/s (see below) | see note |

## Run of record — 2026-09-19

```
loadgen -rate 200000 -senders 12 -duration 15s
```

Machine: 20 cores, Linux 6.8, `net.core.rmem_max` 7.5 MB (the host default
here; raise it in production). Server and load
generator on the same box, so the network is a loopback and both compete for
CPU — a real deployment has more headroom than this, not less.

| | |
|---|---|
| sent | 2 969 769 datagrams in 15.0 s |
| achieved | 197 970 datagrams/s |
| decoded | 2 969 769 (100.00 %) |
| kernel drops | 0 |
| rejected | 0 |
| aggregates written | 1 495 074 |
| CPU | 52.6 s total = **17.5 µs per packet** across all cores, 3.5 of 20 cores busy |
| RSS | 109 MB |

Cardinality during the run: 100 urls × 50 users × 1 account, one minute deep.

### What the cube actually held

```
example/requests: minutes=1 detail=1 dimensionKeys=153 interned=153 capped=0
durable rows:       example/requests: 5000
```

153 map entries in memory against 5 000 rows on disk, for the same three
million packets. That gap is the design working: the cube keeps one
map per **dimension** (100 urls + 50 users + 3 low-cardinality values), not
one entry per **combination**. The combinations live in SQLite, where a query
can still reach them.

## Where the ceiling is

Unpaced, the harness pushes 758 000 datagrams/s at the server. It decodes
about **340 000/s** and the kernel drops the rest — loss equalled kernel drops
exactly, which is what makes the accounting trustworthy. So on this machine:

- **200 000/s** — the product target, zero loss, 3.5 cores.
- **~340 000/s** — where this box saturates.
- Beyond that the limit is the receive buffer and the CPU, not the design;
  more cores or a bigger `rmem_max` moves it.

`intake.read_buffer_bytes` asks for 8 MB and is silently clamped to
`net.core.rmem_max`. Raising that sysctl on the host is the single most
effective thing to do before a burst; the server logs a warning when its
request is clamped.

## Known costs, in order

1. **Decoding is the hot path.** `BenchmarkDecodeMetric` is ~4.4 µs/op because
   a v1 envelope is unmarshalled twice: once for the header to learn `t`, then
   again into the payload. One pass into a flat struct should roughly halve
   it, and `goccy/go-json` is the drop-in after that. Both are worth doing
   only if a deployment needs more than 340 k/s — the target is met without
   them.
2. **The adapter costs about the same again** for legacy packets
   (`BenchmarkAdaptRequest` ~2.9 µs/op), which is why the SDKs exist: a
   platform that speaks v1 natively skips it entirely.
3. **Aggregation is cheap**: `BenchmarkAggregatorAdd` is 315 ns/op, and it is
   what keeps the core's work independent of packet rate.

## The RSS note

109 MB was measured *at* 200 000 packets/s with three million packets in
flight — far beyond ordinary volume. The < 100 MB budget
is for the default retention at ordinary load; at a few hundred
requests/minute the process sits far below it. The number to watch in
production is `hm_build_info`'s neighbours in `/api/metrics`
(`process_resident_memory_bytes`) against the container limit, with
`GOMEMLIMIT` set from that limit.

## Baseline — 2026-09-24

The "before" numbers for the optimisation work. Every later phase
re-runs the same commands and adds its "after" column. Same machine as the
run of record (i9-12900H, 20 threads, Linux 6.8), server on test ports
18081/18082 with a scratch data dir.

### Micro-benchmarks

`go test -run '^$' -bench . -benchmem -count 3`, mean of three.

| Benchmark | Time | Memory | Allocs | Phase |
|---|---|---|---|---|
| `intake` HandleJavaTimestamp | 5.55 µs | 2 064 B | 41 | 5 |
| `intake` HandleZuluTimestamp | 6.21 µs | 2 480 B | 53 | 5 |
| `intake` HandleParallel (20 threads) | 1.08 µs | 2 068 B | 41 | 5 |
| `intake` AggregatorAdd, existing key | 269 ns | 408 B | 4 | 5 |
| `intake` ChunkPartWithFullTable (4 096 pending) | 38.9 µs | 0 | 0 | 1 |
| `protocol` DecodeMetric | 4.60 µs | 1 488 B | 36 | 5 |
| `legacy` AdaptRequest | 3.20 µs | 1 600 B | 20 | 5 |
| `legacy` AdaptMappedRequest | 3.82 µs | 2 272 B | 23 | 5 |
| `state` MinuteViewTotals | 0.81 µs | 576 B | 3 | 2 |
| `state` MinuteViewGroup | 224 µs | 26.7 KB | 169 | 2 |
| `state` MinuteViewGroupSub | 2.05 ms | 185 KB | 1 619 | 2 |
| `state` MarshalMinuteViewGroupSub (65.7 KB/msg) | 189 µs | 65.8 KB | 2 | 2 |
| `state` EstimatedBytes | 102 µs | 0 | 0 | 1 |
| `hub` Flush1Viewer | 14.4 ms | 1.24 MB | 7 063 | 2 |
| `hub` Flush10Viewers | 142 ms | 12.4 MB | 70 651 | 2 |
| `hub` Flush20Viewers | 294 ms | 24.8 MB | 141 288 | 2 |

The state and hub fixtures hold 120 detail minutes of 100 urls × 10 users; a
hub flush sends two dirty minutes to viewers showing five charts each. Flush
cost is linear in viewers — every viewer rebuilds and re-encodes the same
views.

### Load gates (`cmd/loadgen`)

| | Result |
|---|---|
| v1 1 000/s, 10 s | 9 980 sent, 9 980 decoded, 0 lost; RSS 37 MB |
| v0 1 000/s, 10 s | 9 980 sent, 0 lost; RSS 73 MB |
| v1 200 000/s, 12 senders, 15 s | 199 928/s, 0 lost; 37 CPU s = **12.3 µs/packet**; RSS 106 MB |
| unpaced, 8 senders, 10 s | 819 k/s offered, **~428 k/s decoded**, rest kernel drops |

loadgen reports −100 % loss for v0: each legacy request becomes two envelopes
(the `requests` metric and a `dailyLogs` row) and the tool counts one per
datagram. Nothing is lost.

### Soak (`cmd/soak`, `soak.yaml`, 10 min)

5 servers × 5 000 requests/min (legacy v0), 15 alerts, 15 sendLogs, queue
gauges, heartbeats and three v1 signals: `soak.yaml` as it was for this run.
It has since grown to seven signals and faster gauges, so re-measure before
comparing.

| After | 1 min | 5 min | 10 min |
|---|---|---|---|
| RSS | 85 MB | 106 MB | 136 MB |
| hot state | 2.5 MB | 10.3 MB | 20.0 MB |
| database | 18 MB | 75 MB | 137 MB |

Zero kernel drops, decode errors or writer drops; CPU 5.6 % of one core.
Hot state grows ~2 MB/min and had not reached its retention plateau at
10 minutes — the phase 7 soak must run long enough to reach it.

Where the database went in 10 minutes:

| | Size | Rows |
|---|---|---|
| dailyLogs rows | 70.5 MB | 249 990 |
| dailyLogs full-text index | 23.6 MB | |
| dailyLogs indexes | 17.0 MB | |
| aggregates (`agg_dim`, `agg_pair`) | 13.7 MB | 203 926 |

**Daily logs are 89 % of the growth**, ~444 B per request all in. At this
load that is ~16 GB/day, ~80 GB at the 5-day `dailyLogs` retention.

### The local database (`data/hm.db`, pre-overhaul schema)

2.53 GB of pages, no free pages, `auto_vacuum=0`.

| | Size |
|---|---|
| `minute_agg` + index (old schema) | 1 223 MB |
| log rows | 834 MB |
| log full-text indexes | 266 MB |
| log indexes | 206 MB |

Queries on `log_20260923_dailyLogs` (2.84 M rows), best of three, read-only
Python sqlite3:

| Query | Time | Plan |
|---|---|---|
| search, platform only, newest 200 | 327 ms | index on platform, temp B-tree for ORDER BY |
| search, `user=` | 3 471 ms | same |
| search, full-text `soak` | 3 660 ms | FTS scan, temp B-tree for ORDER BY |
| activity recompute scan | 1 526 ms | full scan |

### Web bundle

`vite build`: one chunk, **1 909 KB, 620 KB gzipped**; CSS 0.4 KB.

### After phase 1 — 2026-09-24

| Benchmark | Baseline | Phase 1 |
|---|---|---|
| `intake` ChunkPartWithFullTable | 38.9 µs | **23 ns** |
| `state` EstimatedBytes | 102 µs | **0.3 ns** |
| `hub` Flush20Viewers (batched `events`) | 294 ms | 253 ms |

Everything else within ±8 % run-to-run noise. The 3-minute soak matched
the baseline (0 drops, 0 errors, 0 `tooNew`, 0 `chunksDropped`); the server
logs `hot state memory guard limitMB=512 source=default` at startup.

### After phase 3 — 2026-09-24

On a copy of the local `data/hm.db`:

| | Result |
|---|---|
| one-time compaction at first start | 2 531 MB → **1 286 MB** in 5–6 s (old `minute_agg` gone) |
| second start | no compaction; `auto_vacuum=2`, 0 free pages |
| a 2.84 M-row day of dailyLogs dropped, then started under the 25 k/min soak | **1 242 MB** returned in ~3.5 min, 0 `database is locked`, 0 writer errors or drops |

The first reclaim loop (2 048-page steps back to back, two callers at
startup) starved other writers past the 5 s busy timeout. It now takes
512-page steps (~0.2 s of write lock each), pauses 250 ms between them, and
only one runs at a time.

### After phase 2 — 2026-09-24

| Benchmark | Baseline | Phase 1 | Phase 2 |
|---|---|---|---|
| `hub` Flush1Viewer | 14.4 ms | 12.6 ms | **6.6 ms** |
| `hub` Flush10Viewers | 142 ms | 127 ms | **7.2 ms** |
| `hub` Flush20Viewers | 294 ms | 253 ms | **7.9 ms** |

A flush now builds each distinct view once — one view per chart carrying
every changed minute, ranked once — encodes it once, and assembles each
viewer's message from the encoded bytes. Cost barely grows with viewers.

Live, `soak.yaml` plus 20 WebSocket viewers with five charts each, 3 min:

| | Phase 1 | Phase 2 |
|---|---|---|
| server CPU | 13.0 % of a core | **7.1 %** |
| sent to viewers | 234 MB | 230 MB |

Intake alone is ~5.6 % (baseline soak), so the viewers' share fell from
~7.4 % to ~1.5 %.

### After phase 6 — 2026-09-24

Web bundle (`vite build`, gzipped):

| | Baseline | Phase 6 |
|---|---|---|
| everything | one 1 909 KB chunk, 620 KB | split |
| login screen (`index` + `vendor`) | 620 KB | **207 KB** |
| main app (+ `echarts`, `App`, shared) | 620 KB | **~420 KB** |
| rarely used tabs | in the main chunk | 1–4 KB each, on first click |

ECharts is imported modularly (bar, line, grid, tooltip, legend, inside
zoom, canvas); `vendor` and `echarts` are their own chunks, so a deploy that
only changes the app keeps ~380 KB cached.

Per event: merging keeps every unchanged array and group, each tile's
series are cached per group object, and charts replace only their series
instead of rebuilding (`notMerge` only on a theme, legend or language
change), so a live update redraws only the tiles whose data changed. The
page shell no longer re-renders on every message. Background tabs stop
polling until they are shown again; the wall display keeps polling.

### After phase 4 — 2026-09-24

Log queries on the local copy's 2.84 M-row day (new indexes built on the copy
by hand; new day tables get them from their first row):

| Query | Before | After |
|---|---|---|
| full-text `soak`, newest 200 | 3 515 ms | **1 ms** (FTS5 rowid order, no sort) |
| `user=`, newest 200 | 3 199 ms | **0 ms** (`platform, user, ts`) |
| platform only, newest 200 | 335 ms | **0 ms** (`platform, ts`) |

Same 10-minute soak as the baseline:

| | Baseline | Phase 4 |
|---|---|---|
| database after 10 min | 137 MB | **120 MB** |
| dailyLogs, all in, per request | 444 B | **397 B** |
| — rows | 70.5 MB | 52.0 MB |
| — full-text index | 23.6 MB | 16.6 MB |
| — other indexes | 17.0 MB | 26.8 MB |
| aggregate write transactions | 601 | **51** |

Rows and full-text shrank 26–30 % (lifted fields stored once, values-only
index); the third index (`platform, ts` beside `user` and `account`, all in
ts order) gave some of that back, so logs came out 11 % smaller, not the
30–40 % estimated. RSS at 10 minutes was 162 MB against 136 MB with the heap
unchanged (112 vs 109 MB): SQLite page caches, 8 MB per writer connection
since phase 3 and 4 MB per reader connection now.

### After phase 5 — 2026-09-24

| Benchmark | Baseline | Phase 5 |
|---|---|---|
| `intake` AggregatorAdd, existing key | 269 ns, 408 B, 4 allocs | **109 ns, 0 B, 0 allocs** |
| `intake` HandleJavaTimestamp | 5.55 µs, 2 064 B, 41 allocs | **4.95 µs, 1 632 B, 37 allocs** |
| `intake` HandleZuluTimestamp | 6.21 µs, 2 480 B, 53 allocs | **4.66 µs, 1 632 B, 37 allocs** |
| `intake` HandleParallel | 1.08 µs | 0.94 µs |

36 of the 37 remaining allocations are the double JSON decode, which
"Known costs" above already defers until a deployment needs > 340 k/s.

Load gates on the same box: all pass with nothing lost. At 200 k/s CPU per
packet measured 11.8–12.7 µs across runs of both the phase-4 and phase-5
builds (noise, the load generator shares the machine); the unpaced ceiling
was 335–429 k/s for phase 5 against 338–415 k/s for phase 4. RSS after the
200 k/s run: **74 MB** (baseline 106 MB) — the 16 KB receive buffers are
8 MB for eight readers instead of 32 MB.

## Final — 2026-09-25

### Before and after, in one table

| | Baseline (2026-09-24) | Now |
|---|---|---|
| hub flush, 20 viewers × 5 charts | 294 ms | **7.8 ms** |
| chunk part, full table | 38.9 µs | **23 ns** |
| hot-state size estimate | 102 µs | **0.3 ns** |
| aggregator, existing key | 269 ns, 4 allocs | **107 ns, 0 allocs** |
| packet with `Z` timestamp | 6.21 µs, 53 allocs | **4.58 µs, 37 allocs** |
| log search on a 2.84 M-row day | 0.3–3.7 s | **≤ 1 ms** (new day tables) |
| aggregate write transactions, 10 min soak | 601 | **51** |
| dailyLogs per request, all in | 444 B | **397 B** |
| local `hm.db` | 2 531 MB, never shrinks | **1 286 MB**, freed space returned |
| web bundle, login / app (gzip) | 620 KB / 620 KB | **207 KB / ~420 KB** |
| RSS after 200 k/s | 106 MB | **74 MB** |

### Two hours side by side

The pre-optimisation build (`4e47e9e`) and this one ran at once on the same
box, each with `soak.yaml` and 10 WebSocket viewers, sampled every 5 minutes.

| At 120 min | Before | After |
|---|---|---|
| server CPU, whole run | 458 s | **403 s** (−12 %) |
| database | 1 547 MB | **1 359 MB** (−12 %) |
| messages to viewers | 6 127 | **2 690** (same data, 190 → 182 MB) |
| hot state | 245.5 MB | 245.5 MB |
| RSS | 589 MB | 611 MB |
| drops, errors, kernel drops | 0 | 0 |

Hot state grows ~2 MB/min until the 120-minute detail window is full, in both
builds; with a 20-minute window it went flat at 39.0 MB from minute 20 on,
so it plateaus at `hot_detail_minutes` as designed. RSS is about 2.4× the
hot-state estimate: map overhead plus the collector's headroom.

### Memory under a container limit

Stress: `soak.yaml` plus `loadgen -rate 20000 -urls 10000 -users 10000`, far
above real traffic, in a `systemd-run --scope -p MemoryMax=…`.

| Limit | Guard | Outcome |
|---|---|---|
| 200 MB | 100 MB (half) | **OOM-killed at 6.5 min**, hot state only ~30 MB |
| 512 MB | 256 MB (half) | alive at 25 min; process memory 332 MB and rising with hot state 112 MB — on course to pass the limit before the guard acts |
| 512 MB | 60 MB (config) | guard evicting from minute 13, hot state 52–58 MB, process memory **flat at 230–255 MB** |

Measured: process memory ≈ **120 MB + 2.2 × hot state** under this load.
The guard works; "half the container limit" was too much of it. The
automatic guard is now (0.9 × limit − 120 MB) / 2.2, and Go's memory limit is
90 % of the container limit (unless `GOMEMLIMIT` is set). Same stress again:

| Limit | Guard | Outcome |
|---|---|---|
| 512 MB | **154 MB (fitted)** | alive at 45 min; evicting from minute 35, hot state 137–150 MB, process memory **flat at 392–407 MB**, no OOM kill, 0 drops |

Below a 384 MB container limit the server warns at startup; 512 MB or more is
recommended.

## Reproducing

```bash
make build
./bin/hm &                       # or against a dev server on other ports
go run ./cmd/loadgen -rate 1000 -duration 10s           # the 1 000/s gate
go run ./cmd/loadgen -rate 200000 -senders 12 -duration 15s   # the product target
go run ./cmd/loadgen -rate 0 -senders 8 -duration 10s         # find the ceiling
go run ./cmd/loadgen -legacy -rate 1000 -duration 10s         # the v0 path
```

Micro-benchmarks and the soak:

```bash
go test -run '^$' -bench . -benchmem -count 3 \
  ./internal/intake/ ./internal/protocol/ ./internal/adapter/legacy/ ./internal/state/ ./internal/hub/
go run ./cmd/soak -config soak.yaml -report 1m     # stop with Ctrl-C; add -addr/-status for other ports
```

`loadgen` reads the counters back from `/api/state` before and after, so what
it reports as loss is the server's own accounting, including what the kernel
dropped before the process saw it.
