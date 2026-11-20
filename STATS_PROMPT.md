# Task: Statistics page: storage and traffic charts

## Context

Read `CLAUDE.md` first and follow its rules: ask before each milestone, add a line to `RELEASE.md` after each one, don't commit or push, run `go fix ./...`, keep comments sparse.

The service already has the live map, the tape recorder with playback and moment links (`?t=&speed=`), and the optional SQLite flight history (`internal/history`, schema migrations, `/api/history/*`, `/api/history/timeline`).

This task adds a **Statistics page** with:

1. **Storage:** where disk space goes, how fast it grows, and how long the data fits.
2. **Traffic charts** from hourly rollups that outlive the flight retention: aircraft per hour, an hour × weekday heatmap, registration countries, and max range per day.

## Decisions already made

- **A separate page,** `/stats`, linked from the header. The map page stays as it is.
- **Hourly rollups** in SQLite, kept for a configurable time (`stats.retention`), independent of and normally much longer than `history.retention`.
- **Charts use a library: Apache ECharts** (`echarts`). It's approved as a new frontend runtime dependency, so add it to the list in `FRONTEND_PROMPT.md` and `CLAUDE.md`. Why ECharts: it covers bars, lines *and* heatmaps with one library, supports dark mode, and tree-shakes.
  - Import only what's used, through `echarts/core`: `BarChart`, `LineChart`, `HeatmapChart`, the grid, tooltip, legend, visualMap and dataZoom components, and `CanvasRenderer`.
  - Load the Statistics page as a **lazy chunk** (`React.lazy` + dynamic `import()`), so the map page's bundle doesn't grow. Check this in the `vite build` output and report both chunk sizes.
  - No React wrapper package: a small `useECharts(ref, option)` hook that handles init, `setOption`, resize (`ResizeObserver`) and dispose.
- **Operators: callsign prefix only** (the first 3 letters, when they look like an ICAO airline designator). No airline-name table for now.
- **First set of charts:** storage, aircraft per hour, hour × weekday heatmap, countries, max range per day. Operators, receiver health, altitude distribution and notable aircraft are a later milestone, but the rollups store what they need now (see the schema).

## Rollups (`internal/history`)

- **Schema migration** (the next version after the current one) with these tables:
  ```sql
  CREATE TABLE hourly (
    hour INTEGER PRIMARY KEY,        -- unix seconds, UTC, start of the hour
    aircraft_adsb INTEGER NOT NULL,  -- distinct ICAO addresses with an ADS-B flight active in the hour
    aircraft_modes INTEGER NOT NULL, -- distinct Mode-S-only addresses active in the hour
    flights_started INTEGER NOT NULL,
    positions INTEGER NOT NULL,      -- stored track points in the hour
    messages INTEGER,                -- decoded messages in the hour (live counters; NULL when unknown, e.g. backfill)
    max_range_nm REAL,               -- furthest stored position in the hour
    max_range_bearing REAL,
    db_bytes INTEGER, tape_bytes INTEGER  -- sizes at the end of the hour, for growth rates
  ) WITHOUT ROWID;
  CREATE TABLE hourly_country  (hour INTEGER NOT NULL, country TEXT NOT NULL, aircraft INTEGER NOT NULL, PRIMARY KEY (hour, country))  WITHOUT ROWID;
  CREATE TABLE hourly_operator (hour INTEGER NOT NULL, prefix  TEXT NOT NULL, aircraft INTEGER NOT NULL, PRIMARY KEY (hour, prefix))   WITHOUT ROWID;
  ```
- **Definitions:**
  - "Active in the hour" means the flight overlaps the hour: `first_seen < hourEnd AND last_seen >= hourStart`.
  - Counts are **distinct ICAO addresses**, not flights, so one aircraft circling with a callsign change counts once.
  - Range is computed from stored positions against the configured receiver position.
  - A country is the existing ICAO-block country. A prefix is the first 3 letters of the callsign when it matches `^[A-Z]{3}[0-9]`; anything else isn't counted as an operator.
- **The rollup job** (one goroutine, owned by the history store):
  - At HH:05 each hour, it computes the previous hour in one transaction. The 5-minute delay lets the batched writer flush.
  - It's idempotent: `INSERT OR REPLACE`, so rerunning an hour is safe.
  - `messages` comes from the live stats counter: the job snapshots the counter at each hour boundary and stores the difference. After a restart in the middle of an hour, store NULL for that hour; never a partial number labelled as a full hour.
  - `db_bytes` (the database plus WAL) and `tape_bytes` are sampled when the job runs.
- **Backfill:** at startup, compute any missing hours from the oldest stored flight up to the previous hour, with `messages`, `db_bytes` and `tape_bytes` set to NULL. Run it in the background with a log line for progress, and never block startup.
- **The current hour** is not stored. The API computes it on the fly, so charts include "now".
- **Retention:** once a day, delete rollups older than `stats.retention`. `0` keeps them forever.
- **Without history:** when `history.enabled` is false, there are no rollups. The page shows storage plus a short note on how to enable history.

## Storage (`internal/stats` or a small new package)

- `GET /api/storage` returns:
  - **volume:** `totalBytes` and `freeBytes` of the filesystem holding the state dir, from `statfs`. Use build tags for Linux and macOS; other systems return the fields as unknown (omitted).
  - **usage:** `tapeBytes`, `dbBytes` (including `-wal`/`-shm`), `otherBytes` (the rest of the state dir, for example `coverage.json`)
  - **growth:** `tapeBytesPerDay` and `dbBytesPerDay`, from the `hourly` size samples over the last 24 hours. Fall back to the tape segment sizes of the last 24 hours when there are no samples yet. Omit when unknown.
  - **projection:**
    - `tapeSteadyStateBytes` = growth per day × `tape.retention` days
    - `dbSteadyStateBytes`, likewise with `history.retention`
    - `daysUntilFull`: free space ÷ total growth per day, only meaningful when a retention is 0
    - `daysUntilGuard`: until free space would reach `tape.minFreeMB`
  - **horizon:** `tapeOldest`, `flightsOldest`, `rollupsOldest`
  - **flags:** `guardActive` (recording paused for space), `guardDeletedSegments` (count since start), and `warning` (amber when free space is below 2 × `tape.minFreeMB`)
- The values are cheap enough to compute per request. Cache them for 60s anyway.

## Statistics API

All of these take `from`/`to` (RFC 3339 or unix ms) and return JSON 503 when history is disabled. Cap the range at the rollup retention, and cap the row count.

- `GET /api/stats/hourly?from=&to=`: the `hourly` rows (camelCase JSON) plus the computed current hour, marked `partial: true`.
- `GET /api/stats/countries?from=&to=&limit=20`: `[{country, aircraft}]`, summed over the range. Note in the README that a sum over hours counts an aircraft once per hour it was seen, so this is "aircraft-hours", unless you compute distinct counts from `flights` when the range is still inside `history.retention`. Pick one, document it, and label the chart accordingly.
- `GET /api/stats/operators?from=&to=&limit=20`: the same for prefixes. The API comes now; the chart comes later.
- `GET /api/stats/range-daily?from=&to=`: the max range per **local** day. Take the timezone as a `tz` parameter (an IANA name from the browser) and validate it.
- Times are stored in UTC. The hour × weekday heatmap is aggregated **in the browser** from hourly rows, in the browser's local timezone, so it shows local rhythm.

## The page (`web/src/stats/`)

- **Routing without a router package:** `location.pathname === '/stats'` renders the Statistics page, otherwise the map. The header link uses `history.pushState`, and `popstate` switches back. The SPA fallback already serves `index.html` for `/stats`. Keep existing query parameters intact.
- **Range picker:** 24 h, 7 days, 30 days, 1 year, and custom. Keep it in the URL (`?range=7d` or `from`/`to`) so a stats view can be shared.
- **Storage panel**, at the top:
  - a stacked bar: tape, database, other, free
  - growth per day and the steady-state projections in plain words ("tape levels off at about 0.5 GB after 14 days")
  - the oldest tape and oldest flight
  - amber and red warnings from the flags
- **Charts:**
  1. **Aircraft per hour:** stacked bars, ADS-B vs Mode-S-only, with `dataZoom` for long ranges; the partial current hour hatched or lighter.
  2. **Hour × weekday heatmap:** 7 × 24 cells with the average aircraft per hour over the range, in local time.
  3. **Countries:** a horizontal bar chart of the top 15, with the rest summed as "Other".
  4. **Max range per day:** a line chart, with a marker for the best day. This is the antenna before/after comparison.
- **Click-through:** clicking an hour bar or a heatmap cell opens the map in playback at that hour (`/?t=<hour start>`). Heatmap cells aggregate many weeks, so a cell opens the most recent matching hour.
- **Theme:** light and dark, from the existing CSS tokens. Pass the colors into the ECharts options; don't rely on ECharts' built-in themes. Charts must be readable in both.
- **Empty and loading states:** "No data yet: rollups start after the first full hour" and similar. Never an empty frame.
- Everything goes through the stats APIs, with no polling faster than 60s. The page doesn't use the live WebSocket.

## Config

Add to `internal/config` (`Default()`, validation, and `config/config.example.yaml`, which a test requires to equal `Default()`), using camelCase keys:

```yaml
stats:
  # Hourly rollups (aircraft per hour, countries, operators, range) need history.enabled.
  # They are kept this long, independently of history.retention; 0 keeps them forever.
  retention: 8760h
```

## Privacy rule

**Never put a real receiver position in code, tests, fixtures, docs or examples.** Use the placeholder coordinates from `config/config.example.yaml`. The stats page shows ranges and bearings, not the receiver position. Screenshots in the README must not show the real map location: use `make demo`.

## Tests

- **Rollups:**
  - distinct counting (one aircraft over two flights in an hour counts once; a flight spanning three hours counts in each)
  - ADS-B vs Mode-S split
  - country and prefix extraction, including non-airline callsigns
  - max range and bearing
  - idempotent reruns
  - backfill fills only missing hours and doesn't block startup
  - retention
  - `messages` is NULL after a mid-hour restart
- **Storage:** `statfs` behind an interface with a fake in tests; growth and projection arithmetic, including the zero, unknown and retention-0 cases; warning thresholds.
- **API:** range parsing and caps; the partial current hour; `tz` validation; 503 without history; the contract fixtures for the new JSON, as `CLAUDE.md` requires.
- **UI** (Vitest):
  - the heatmap aggregation in local time, including a DST change (use a fixed IANA zone in tests)
  - range-picker URL round trip
  - routing between `/` and `/stats`
  - the click-through URL for bars and cells
  - storage text formatting
  - the `useECharts` hook disposes on unmount
- `go test -race ./...`, `go vet`, `gofmt`, `go fix`, `npm test`, `npm run lint`, `npm run typecheck` all clean. `make dist` stays static; report the map and stats chunk sizes.

## Milestones

1. **Rollups:** the schema migration, the hourly job, message-counter snapshots, size samples, backfill, retention, the `stats` config section, tests.
2. **API:** `/api/storage` (`statfs`, growth, projections, flags) and the `/api/stats/*` endpoints, with contract fixtures and tests.
3. **Page:** routing, the lazy chunk with tree-shaken ECharts, the range picker, the storage panel and the four charts, click-through to playback, light and dark themes, empty states, tests.
4. **Docs:** README (the statistics page, what each chart counts, the rollup retention, disk projections) and `CLAUDE.md` architecture notes and dependency lists.
5. **Later, only after I confirm:** the operators chart, receiver health per hour, altitude distribution, and notable aircraft (emergencies; first-ever-seen ICAO addresses).
