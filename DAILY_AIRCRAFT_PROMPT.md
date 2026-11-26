# Task: Daily aircraft totals and "new to us" on the Statistics page

## Context

Read `CLAUDE.md` first and follow its rules: ask before each milestone, add a line to `RELEASE.md` after each one, don't commit or push, run `go fix ./...`, keep comments sparse.

The Statistics page already shows hourly rollups (`hourly`, `hourly_country`, `hourly_operator`, `hourly_altitude`), max range per day (`/api/stats/range-daily?tz=`), and notable events. The `aircraft_seen` table holds one row per ICAO address (`first_seen`, `last_seen`, flights, ADS-B, last callsign). It's maintained by the rollup job and on flight close, and **never deleted**: neither `history.retention` nor `stats.retention` prunes it.

**Goal:** for each day, show:
1. **how many different aircraft** (distinct ICAO addresses) were recorded that day
2. **how many of them were new to us**, meaning seen that day for the first time

**Why a new rollup:** daily distinct counts can't be built by adding up the hourly rows, because an aircraft seen in 5 hours counts 5 times there. So this needs its own small per-day table.

## Definitions (document them on the page and in the README)

- **Day:** a calendar day in the configured timezone `stats.timezone` (below).
- **Recorded that day:** the aircraft has at least one flight overlapping the day (`first_seen < dayEnd AND last_seen >= dayStart`). This counts ADS-B and Mode-S-only aircraft alike, with a split between the two.
- **New to us:** the aircraft's `aircraft_seen.first_seen` falls within that day.
  - `aircraft_seen` is never deleted, so "new" means truly first seen since recording began. Say this in the chart's help text.
  - Don't add pruning of `aircraft_seen` as part of this task.
- **Returning:** recorded that day minus new.
- **Learning period:** at the start of recording, almost everything is "new". Mark the first 7 days after the oldest stored data (the earliest `aircraft_seen.first_seen`) as `learning: true`. The UI shows them hatched, with a note, so the high new-aircraft numbers there aren't misread.

## Backend (`internal/history`)

- **Config:** `stats.timezone`, an IANA name. Empty means the server's local timezone (`time.Local`); the systemd unit and Docker image may run in UTC, so the README should recommend setting it explicitly. Validate it at load. Update `Default()` and `config/config.example.yaml`.
- **Schema migration** (the next version):
  ```sql
  CREATE TABLE daily_aircraft (
    day TEXT NOT NULL,              -- 'YYYY-MM-DD' in stats.timezone
    icao TEXT NOT NULL,
    adsb INTEGER NOT NULL,          -- sent ADS-B on that day
    PRIMARY KEY (day, icao)
  ) WITHOUT ROWID;
  CREATE INDEX daily_aircraft_icao ON daily_aircraft(icao, day);
  ```
  This is one row per aircraft per day, a few hundred rows a day, which keeps it small even over a year. It also gives the aircraft-history feature a true "days seen" that outlives flight retention. Use it there if `AIRCRAFT_HISTORY_PROMPT.md` is already implemented.
- **Maintenance:**
  - The hourly rollup job also does `INSERT OR IGNORE` into `daily_aircraft` for every aircraft active in the hour, using the day (or days) that hour belongs to in `stats.timezone`. A DST change can make an hour belong to two dates; handle it and test it.
  - `adsb` is OR-ed: once true for the day, it stays true.
- **Backfill:** at startup, in the background with a progress log and without blocking startup, fill `daily_aircraft` from all stored flights.
  - If `stats.timezone` changed since the last run, rebuild it from the stored flights. Days older than `history.retention` then can't be rebuilt; say so in the log and the API (`approximate: true` for those days).
  - Keep the timezone used in `meta`.
- **Retention:** prune `daily_aircraft` with `stats.retention`, like the other rollups.
- **Today:** comes from the same table, which is filled hourly, plus the current hour's live aircraft computed on the fly. Mark it `partial: true`.

## API

- **`GET /api/stats/daily?from=&to=`:** one row per day in `stats.timezone`, using the existing range parsing and caps:
  ```json
  {"day":"2026-11-25","aircraft":214,"adsb":171,"modeS":43,
   "new":38,"newAdsb":31,"newModeS":7,"returning":176,
   "learning":false,"partial":false,"approximate":false}
  ```
  Also return the timezone used: `{"timezone":"…","days":[…]}`.
- **`GET /api/stats/new?day=YYYY-MM-DD&limit=200`:** the aircraft that were new on that day, as `[{icao, callsign, country, firstSeen, adsb}]`, ordered by `firstSeen`. Take the callsign from their first flight that day.
- Both return JSON 503 when history is disabled. Add contract fixtures and TS types as `CLAUDE.md` requires.

## UI (`web/src/stats/`)

- **Tiles** at the top of the traffic section: **Today** (partial) and **Yesterday**, each showing *aircraft*, *new*, and *% new*, with the ADS-B / Mode-S split in small text.
- **"Aircraft per day" chart** (ECharts):
  - stacked bars per day: **new** in the accent color, **returning** muted
  - a line for **% new** on a second axis
  - learning-period days hatched, with a legend note; today lighter (partial)
  - `dataZoom` for long ranges
  - follows the existing range picker
  - the tooltip shows every field of the day, and states the timezone when it differs from the browser's
- **Click on a day** opens a **"New on <day>"** list under the chart, from `/api/stats/new`: ICAO, callsign, country, first-seen time, ADS-B/Mode-S. Each row links to the aircraft-history panel on the map page (`/?history=<icao>`) when that feature exists; otherwise to playback at its first-seen time (`/?t=…&icao=…`).
- Empty and loading states like the existing charts: "No data yet: daily totals start after the first full hour".

## Privacy rule

**Never put a real receiver position in code, tests, fixtures, docs or examples.** Fixtures use fictional ICAO addresses and callsigns. README screenshots come from `make demo`.

## Tests

- **Rollup:**
  - an aircraft in several hours or flights on one day counts once
  - a flight crossing midnight counts on both days
  - `adsb` is OR-ed per day
  - "new" comes from `aircraft_seen.first_seen`
  - an aircraft absent for longer than `stats.retention` is still *not* new when it returns (`aircraft_seen` is never pruned)
  - DST days in a fixed IANA zone (one 23-hour and one 25-hour day)
  - a timezone change triggers a rebuild, with `approximate` marking days beyond flight retention
  - the backfill doesn't block startup
  - retention pruning
- **API:** the day rows, including `learning`, `partial` and `approximate`; `/new` ordering and the limit; 503 without history; fixtures.
- **UI** (Vitest): the tiles (today partial, % rounding, divide-by-zero), the chart series and hatching, the day click → list → links, and the timezone note.
- `go test -race ./...`, `go vet`, `gofmt`, `go fix`, `npm test`, `npm run lint`, `npm run typecheck` all clean.

## Milestones

1. **Rollup:** `stats.timezone`, the `daily_aircraft` migration, hourly maintenance, backfill and rebuild on a timezone change, retention, tests.
2. **API:** `/api/stats/daily` and `/api/stats/new`, fixtures, tests.
3. **UI and docs:** the Today/Yesterday tiles, the "Aircraft per day" chart, the "New on <day>" list with links, README definitions (day, new, learning period, retention caveat), `CLAUDE.md` notes.
