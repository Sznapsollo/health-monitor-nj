# healthMonitorNJ wire protocol v1

**Status: frozen.** Additions will only ever be optional fields; anything that
would break a sender gets a new version number in `v`. Written 2026-09-19.

Send UDP datagrams of JSON to the monitor. Fire and forget: there is no ack,
no retry and no back channel, and a sender must never block on the monitor
being reachable.

- **Transport**: UDP, default port **8082**, UTF-8, one JSON object per
  datagram (or an array of them, see [Batching](#batching)).
- **Size**: keep a datagram under ~1400 bytes to stay inside a typical MTU;
  up to 64 KB works on a local network, and anything larger must be
  [chunked](#chunking).
- **Authentication**: none today. The `auth` field is defined and ignored, so
  a shared token can be switched on later without changing any sender.

The machine-readable schema is
[`schema/hm-protocol-v1.json`](../schema/hm-protocol-v1.json), generated from
the Go structs in `internal/protocol`. The TypeScript types the browser uses
are generated from that same file, so the two ends cannot drift.

## The envelope

Every datagram carries these fields:

| Field | Type | Required | Meaning |
|---|---|---|---|
| `v` | integer | **yes** | Protocol version. Always `1`. A datagram without `v` is treated as legacy (v0) and handed to a platform adapter. |
| `t` | string | **yes** | `metric`, `gauge`, `status`, `alert`, `log` or `chunk`. |
| `platform` | string | no | Which system this is about. Resolved from the listening port or the sender's address when absent. |
| `source` | string | no | Host or instance name of the sender, e.g. `web-1`. |
| `ts` | string | no | Event time. Server time is used when absent. |
| `auth` | string | no | Reserved. Ignored today. |

### Timestamps

`ts` is parsed leniently, because senders disagree about the format:

```
2026-09-19T10:11:12.123+02:00   ISO 8601 with a colon in the offset
2026-09-19T10:11:12.123+0200    Java's SimpleDateFormat "Z" — no colon
2026-09-19T10:11:12Z            UTC
1758182472123                   epoch milliseconds (unquoted number)
```

Send an offset. A timestamp without one is read as local to the server, which
is only correct by accident.

Packets are bucketed into whole minutes by their `ts`. A packet whose
timestamp is far in the past can be rejected by the server's
`intake.drop_older_than` rule, and one stamped more than `intake.drop_newer_than`
(2 minutes by default) ahead of the server's clock is rejected as `tooNew`;
both are counted, never dropped silently. Keep sender clocks in sync (NTP).

## Envelope types

### `metric` — something happened, count it and time it

The workhorse: one row per request, job, transaction or anything else worth
charting over time.

```json
{"v":1,"t":"metric","signal":"requests","source":"web-1",
 "ts":"2026-09-19T10:11:12.123+02:00",
 "dims":{"url":"/api/work/groups","user":"anna","account":"42"},
 "values":{"count":1,"ms":142}}
```

| Field | Type | Meaning |
|---|---|---|
| `signal` | string | Which stream this belongs to, e.g. `requests`, `jobs`. |
| `dims` | object of strings | What to group by. Only the dimensions the signal declares are kept; anything else is ignored, so an extra field cannot blow up the server's memory. |
| `values` | object of numbers | `count` (default 1) and `ms` (the duration). |

**Pre-aggregating.** A busy sender should not send one datagram per request.
Add them up per second per distinct `dims` and send one datagram:

```json
{"v":1,"t":"metric","signal":"requests","dims":{"url":"/api/x"},
 "values":{"count":120,"ms":8400,"samples":120,"minMs":12,"maxMs":410}}
```

`ms` is then the **sum**, and `samples` says how many measurements it covers.
Without `samples` the packet counts as one measurement of `ms` — which is
right for a `jobReport`-style packet ("3 items, took 50 ms") and wrong for a
sum. `minMs` and `maxMs` are optional and recommended when pre-aggregating.

### `gauge` — the current value of something

```json
{"v":1,"t":"gauge","signal":"queueDepth","source":"jobs-1",
 "points":[{"label":"mail","value":12},{"label":"invoices","value":3,"warn":true}]}
```

Each point has a `label`, a `value`, an optional `warn` flag and an optional
`unit`. Only the latest reading per `source` is kept.

### `status` — this thing is alive, and here is its state

```json
{"v":1,"t":"status","signal":"servers","key":"web-1",
 "payload":{"branch":"master","build":"abc1234","startedAt":"2026-09-19T08:00:00+02:00"}}
```

`key` identifies the thing (a server, a worker, a queue). Send one on a timer;
the monitor raises an alert when a known key goes quiet for longer than its
configured `offlineAfterSeconds`.

### `alert` — a human may need to look at this

```json
{"v":1,"t":"alert","level":"ERROR","category":"payments",
 "message":"Could not reach the payment gateway","groupKey":"gateway-timeout",
 "data":{"exception":"SocketTimeoutException","attempt":3}}
```

| Field | Meaning |
|---|---|
| `level` | `ERROR`, `WARN`, `INFO`, `DEBUG` or `TRACE`. |
| `category` | Your own grouping label. |
| `message` | What happened, in words. |
| `groupKey` | Repeats sharing this key within one minute collapse into a single entry with a count — use it for anything that can fire in a storm. |
| `data` | Anything else worth keeping. |

### `log` — a searchable row

```json
{"v":1,"t":"log","signal":"sendLogs","key":"2026-09-19","level":"INFO",
 "data":{"to":"user@example.test","subject":"Order confirmation","emailType":"orderConfirmation"}}
```

`signal` names the log stream; `key` is an optional grouping key. `data` is
free-form and searchable.

### `chunk` — a payload too big for one datagram

```json
{"v":1,"t":"chunk","id":"a7f3","part":1,"of":3,"data":"{\"v\":1,\"t\":\"log\"…"}
```

Split the JSON text of the real envelope into `of` parts, number them from 1,
and give every part the same `id`. The monitor joins them in order and decodes
the result. Incomplete sets are discarded after a minute.

## Batching

Any datagram may hold a JSON **array** of envelopes instead of one:

```json
[{"v":1,"t":"metric","signal":"requests","dims":{"url":"/a"},"values":{"count":3,"ms":90}},
 {"v":1,"t":"metric","signal":"requests","dims":{"url":"/b"},"values":{"count":1,"ms":12}}]
```

Every envelope in the array is complete in its own right. Batching plus
pre-aggregation is what keeps a busy platform to a few datagrams per second.

## Signals are configuration, not code

A signal name is not registered in advance by the monitor's operator writing
Go: it is declared in that platform's catalogue file
(`platforms/<platform>/signals.yaml`), which also says which dimensions to
keep, how long to keep them and how the charts should look. With
`autoRegister` on, an unknown `metric` or `gauge` signal creates itself with
sensible defaults on first sight.

An unknown signal is never dropped silently: it is counted, the last hundred
such packets are kept for inspection, and an internal alert fires.

## Worked example: a shell sender

```bash
printf '{"v":1,"t":"metric","signal":"requests","dims":{"url":"/api/x"},"values":{"count":1,"ms":42}}' \
  | nc -u -w1 monitor-host 8082
```

That is the whole integration. Any language that can build a JSON string and
open a UDP socket is a supported sender; `pkg/client` (Go) and
`hm-client-java` exist to save you the typing.

## Versioning

- `v: 1` is frozen. New **optional** fields may be added; existing fields will
  not change meaning, type or name.
- A datagram with no `v` is protocol v0: the flat legacy format, still
  accepted through an adapter and documented only in
  `internal/adapter/legacy`. Do not write new senders against it.
- A future binary framing would be `v: 2` and would be negotiated by nothing
  at all — senders simply choose. v1 stays supported.
