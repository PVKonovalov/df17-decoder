# Task: Server-side track buffer and optional SQLite flight history

## Context

Read `CLAUDE.md` first and follow its rules: ask before each milestone, add a line to `RELEASE.md` after each one, don't commit or push, run `go fix ./...`, keep comments sparse.

Today, trails exist only in the browser (`web/src/state/reducer.ts`, `extendTrail`, a fixed 5 minutes). A newly opened window shows each aircraft's path only from the moment it connected, and nothing survives a restart.

This task adds three things:

1. **Track buffer in the server.** The service keeps recent track points per aircraft, so any newly opened window immediately shows the last N minutes of every trail. N is configurable.
2. **Optional flight history in SQLite.** When enabled, every thinned track point (and the altitude/squawk history of Mode-S-only aircraft) is written continuously in batches to an SQLite database, grouped into flights.
3. **Playback-ready storage.** Playback in the UI is a later task and not part of this one. The schema and indexes must support it now: "state of all aircraft at time T", and "all points between T1 and T2".

## Decisions already made

- **Continuous batched writes,** not flush-on-expiry. A writer goroutine inserts buffered points every `history.flushInterval` in one transaction. Reasons: a crash or restart must not lose hours of tracks, and later playback of "10 minutes ago" must include aircraft that are still in the air.
- **The live UI shows the last N minutes of each trail** (`trails.window`, configurable). No full-flight trail in the live view.
- **Mode-S-only aircraft are stored too.** They get a `flights` row plus their altitude and squawk history, even though they have no positions.
- **SQLite driver: `modernc.org/sqlite`** (pure Go, no cgo). This is approved as a new backend dependency, so the static `make dist` binaries and the distroless Docker image keep working. Add it to the approved list in `CLAUDE.md`.

## Design

### Track points and thinning (`internal/tracker`)

- Add a compact `TrackPoint` type with plain values, no pointers: data time (unix ms), lat, lon, altitude (baro, else GNSS; a flag says which, or "unknown"), ground speed, track, vertical rate, on-ground. Use data time, as the tracker already does for ages, so fast replays behave correctly.
- Points come from `updatePosition`, after the existing range and speed checks, so rejected fixes never enter a trail.
- **Thinning:** keep a new point only if at least one of these is true:
  - `trails.minInterval` (default 5s) has passed since the last kept point
  - the track has changed by ≥ 3°
  - the altitude has changed by ≥ 200 ft
  - on-ground has changed

  Keep the turn and altitude thresholds as named constants. Thinning applies to both the UI trail and the database, so they show the same path.
- The in-memory trail per aircraft is trimmed to `trails.window` (default 10m) of data time before its newest point. The database receives every kept point before trimming, so memory stays small however long a flight is.
- Concurrency: the tracker keeps a single writer. Snapshots return copies of trails, never shared slices.

### Flights

- A flight is one continuous presence of one ICAO address:
  - It **opens** on the first accepted message from an address that has no open flight.
  - It **stays open** while the aircraft is in the tracker, and for `history.flightGap` (default 10m) after it expires (`tracker.ttl`). If the same address is heard again within the gap, the same flight continues. The in-memory trail continues too, trimmed by `trails.window` as usual.
  - It **closes** when the gap passes without messages, or when the callsign changes to a different non-empty value. A callsign change closes the old flight and opens a new one at that moment.
- Mode-S-only aircraft follow the same rules. Their history is samples of altitude and squawk, recorded when the altitude changes by ≥ 100 ft, the squawk changes, or at least 30s have passed since the last sample.
- Flight segmentation lives in the tracker (or a small package next to it). It runs even when history is disabled, but writes nothing then.

### WebSocket and UI (`internal/api/ws.go`, `web/src`)

- On connect, after `receiver` and `map`, send a new message `{type:"trails", data:{window:"10m", trails:{"<icao>":[points…]}}}` with every current trail.
- After that, send `{type:"trailPoints", data:{"<icao>":[new points since the previous push]}}` with every `aircraft` push. Track per connection what was already sent; don't resend whole trails.
- `/api/stream` (SSE) and `/api/aircraft` are unchanged. Optionally add `GET /api/aircraft/{icao}/trail`.
- UI: the reducer stops building trails from snapshots. It takes the `trails` message as the initial state and appends `trailPoints`, trimming to the window from the server (not a hard-coded 5 minutes). Trails of aircraft that disappear from the snapshot are dropped, as today. `AircraftLayer` keeps redrawing a trail only when its array identity changes.
- Update `api/types.ts`, `toAction` (unknown types are still ignored), the reducer tests, and the contract fixtures as `CLAUDE.md` requires for API changes.

### SQLite history (`internal/history`, new)

- Off by default. Enabled with `history.enabled: true`.
- Open the database with WAL mode, `synchronous=NORMAL`, and a busy timeout. One writer goroutine owns all writes, and HTTP reads use a separate read-only connection.
- The tracker hands points, samples and flight open/close events to the writer through a bounded channel. **It must never block the decode loop:** when the channel is full, drop the item and count it.
- Every `flushInterval` (default 15s), the writer inserts everything queued in one transaction. Flush once more on shutdown.
- Schema (adjust names if you have a reason; keep the indexes):
  ```sql
  CREATE TABLE flights (
    id INTEGER PRIMARY KEY,
    icao TEXT NOT NULL, callsign TEXT, squawk TEXT, category TEXT, country TEXT,
    adsb INTEGER NOT NULL,          -- 0 for Mode-S-only
    first_seen INTEGER NOT NULL,    -- unix ms, data time
    last_seen INTEGER NOT NULL,
    closed INTEGER NOT NULL DEFAULT 0,
    points INTEGER NOT NULL DEFAULT 0,
    max_alt INTEGER, min_distance_nm REAL
  );
  CREATE INDEX flights_time ON flights(first_seen, last_seen);
  CREATE INDEX flights_icao ON flights(icao, first_seen);

  CREATE TABLE positions (
    flight_id INTEGER NOT NULL REFERENCES flights(id),
    t INTEGER NOT NULL,             -- unix ms, data time
    lat REAL NOT NULL, lon REAL NOT NULL,
    alt INTEGER, alt_gnss INTEGER NOT NULL DEFAULT 0,
    gs REAL, track REAL, vr INTEGER, on_ground INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (flight_id, t)
  ) WITHOUT ROWID;
  CREATE INDEX positions_t ON positions(t);

  CREATE TABLE samples (            -- altitude/squawk history, mainly for Mode-S-only flights
    flight_id INTEGER NOT NULL REFERENCES flights(id),
    t INTEGER NOT NULL, alt INTEGER, squawk TEXT,
    PRIMARY KEY (flight_id, t)
  ) WITHOUT ROWID;

  CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT);  -- schema version
  ```
- **Migrations:** store a schema version in `meta` and apply numbered migrations at startup. Version 1 is the schema above.
- **Crash recovery:** at startup, close any flight left `closed = 0` with `last_seen` set to its newest stored point.
- **Retention:** once an hour, delete flights (with their positions and samples) whose `last_seen` is older than `history.retention` (default 30 days; `0` keeps everything). Then run `PRAGMA incremental_vacuum` (create the database with `auto_vacuum=INCREMENTAL`).
- **Stats:** add `historyFlightsOpen`, `historyPointsWritten`, `historyDropped`, `historyWriteErrors` and `historyLastFlush` to `/api/stats`. Log write errors once per failure streak and log recovery, like `saveCoverage`.
- **Config validation:** the `history.file` directory must be writable (reuse `checkWritableDir`), otherwise it's a load error (exit 2). A relative path resolves against the working directory, which is the state dir under systemd. In Docker it lives under `/var/lib/df17-decoder`.

### Read API (verification only, no UI)

Read-only endpoints, so the stored data can be checked and the later playback task has a base. They return a JSON 404/503 when history is disabled:

- `GET /api/history/flights?from=&to=&icao=`: flights that overlap the time range (RFC 3339 or unix ms), newest first, with a limit.
- `GET /api/history/flights/{id}`: one flight with its positions and samples.
- `GET /api/history/at?t=`: for each flight open at `t`, its last position (or sample) at or before `t`, if not older than `tracker.ttl`. This is the core playback query. Test it explicitly.

### Config

Add sections to `internal/config` (`Default()`, validation, and `config/config.example.yaml`, which a test requires to equal `Default()`). Use camelCase keys, as in the rest of the file:

```yaml
trails:
  # Length of each aircraft's trail kept in memory and sent to the UI (data time).
  window: 10m
  # Minimum time between kept track points on a straight, level leg.
  minInterval: 5s

history:
  # Write flights and thinned track points to an SQLite database.
  enabled: false
  # Relative to the working directory (the state dir under systemd); Docker: /var/lib/df17-decoder/history.db
  file: history.db
  # Batch interval for database writes.
  flushInterval: 15s
  # A gap longer than this after an aircraft expires closes its flight.
  flightGap: 10m
  # Flights older than this are deleted hourly; 0 keeps everything.
  retention: 720h
```

## Privacy rule

**Never put a real receiver position in code, tests, fixtures, docs or examples.** Use the existing placeholder coordinates from `config/config.example.yaml`, or values derived from the configured receiver at run time. Add `*.db`, `*.db-wal` and `*.db-shm` to `.gitignore`. The database reveals where the receiver is, just like `coverage.json`.

## Tests

- **Thinning:** a straight leg keeps one point per `minInterval`; a turn or a climb keeps extra points; on-ground changes are always kept.
- **Trail window:** trimming by data time; a fast file replay produces the same trails as real time.
- **Flight segmentation:** reappearing within the gap continues the flight; after the gap, a new one opens; a callsign change splits it; Mode-S-only aircraft get flights and samples.
- **WebSocket:** a new connection gets the `trails` message with the current trails, then only new points in `trailPoints`; nothing is resent.
- **History store** (temp-dir database):
  - schema and migrations from an empty file
  - batched writes
  - a full channel drops and counts instead of blocking
  - crash recovery of open flights
  - retention deletes old flights with their rows
  - the `/api/history/at` query against a known set of points
- **Integration:** replay `testdata/capture.txt` with history enabled into a temp database; check the flight and point counts, and that the decoder counts asserted in `internal/app` tests didn't change.
- **UI** (Vitest): the reducer handles `trails` + `trailPoints`, window trimming from the server value, and dropping trails of gone aircraft. Update the contract test.
- `go test -race ./...`, `go vet`, `gofmt`, `go fix`, `npm test`, `npm run lint`, `npm run typecheck` all clean.

## Milestones

1. **Track buffer:** `TrackPoint`, thinning, trail window, flight segmentation (gap + callsign split, Mode-S samples), `trails` config section, tests. No API change yet.
2. **WebSocket + UI:** `trails` and `trailPoints` messages; the reducer uses server trails with the configurable window; contract and reducer tests; check that a newly opened window shows existing trails immediately (`make demo`).
3. **SQLite store:** `internal/history` with `modernc.org/sqlite`, schema v1 + migrations, non-blocking batched writer, crash recovery, retention, stats, `history` config section, `.gitignore`, tests.
4. **Read API:** the three `/api/history` endpoints, with tests for the `at` query.
5. **Docs and deployment:**
   - README (feature, config, disk use estimate, privacy note)
   - `CLAUDE.md` architecture notes and the approved-dependency list
   - `deploy/df17-decoder.service`, if the state dir needs anything new
   - Dockerfile/volume notes
   - `make dist` still produces static binaries for all three architectures, with no cgo
