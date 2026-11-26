# Task: History of a selected aircraft

## Context

Read `CLAUDE.md` first and follow its rules: ask before each milestone, add a line to `RELEASE.md` after each one, don't commit or push, run `go fix ./...`, keep comments sparse.

The service already has:
- live selection, through a table row or a map marker
- the detail panel, which stays open with "No longer received" when an aircraft leaves
- the SQLite flight history (`/api/history/flights?icao=&q=`, `/api/history/flights/{id}` with positions and samples)
- the tape with playback and moment links
- the deck's "find a flight", which seeks playback
- ECharts on the Statistics page

**Goal:** for one aircraft (one ICAO address), show its whole recorded history: a summary, the list of its flights, and their tracks on the map. Three entry points open it:
1. **Search** by ICAO address or callsign, including aircraft that aren't currently received.
2. **A click on a table row.**
3. **A click on a map marker.**

With `history.enabled: false`, all history UI is hidden and the existing behavior is unchanged.

## Backend

### Aircraft summary that outlives flight retention

- **Extend the existing `aircraft_seen` table** (`icao`, `first_seen`, `last_seen`; it already exists, is backfilled from `flights`, updated by the rollup job, and pruned by `stats.retention` on `last_seen`). **Don't create a second table.** Add a schema migration with:
  ```sql
  ALTER TABLE aircraft_seen ADD COLUMN flights INTEGER NOT NULL DEFAULT 0;
  ALTER TABLE aircraft_seen ADD COLUMN adsb INTEGER NOT NULL DEFAULT 0;   -- ever sent ADS-B
  ALTER TABLE aircraft_seen ADD COLUMN last_callsign TEXT;
  ALTER TABLE aircraft_seen ADD COLUMN country TEXT;
  ```
  - The migration backfills the new columns from the flights still stored.
  - From then on they're kept up to date wherever `aircraft_seen` is already maintained, and when a flight closes.
  - The table outlives `history.retention`, so "first seen" and "total flights" stay true after old flights are deleted. A row is only removed when the aircraft hasn't been seen for `stats.retention`, as today. Say so in the summary, for example "first seen within the last 365 days".
- Add an index for callsign search: `CREATE INDEX flights_callsign ON flights(callsign, first_seen)`.

### Endpoints

All of these return JSON 503 when history is disabled. Update the contract fixtures and TS types as `CLAUDE.md` requires.

- **`GET /api/history/search?q=&limit=20`:** finds **aircraft, not flights**.
  - A 1–6 character hex `q` matches ICAO addresses by prefix.
  - Any `q` also matches callsigns by prefix, and by substring for 3 or more characters, case-insensitive.
  - Results come one per ICAO address, ordered with live first, then by last seen: `{icao, lastCallsign, callsigns[] (up to 5 most recent distinct), country, lastSeen, flights, adsb, live}`.
  - `live` comes from the tracker.
- **`GET /api/history/aircraft/{icao}`:**
  - from `aircraft_seen`: first and last seen ever, the total flight count, the country, whether it ever sent ADS-B
  - computed from the flights still stored: every callsign with its flight count, max altitude, closest and furthest distance from the receiver, the number of distinct days seen, and the position quality summary if the flight table has it
  - `retainedFrom`: the oldest flight still stored, so the UI can say "details for the last 30 days"
- **`GET /api/history/flights?icao=&before=&limit=`:** the existing endpoint, plus **cursor paging** with `before` (a `first_seen` value). Newest first. Each row gets `durationS`, `maxAlt`, `minDistanceNm`, `maxDistanceNm`, `points`, and `onTape`: true when the tape still covers that flight's time, so the UI can enable "Play".
- **`GET /api/history/flights/{id}`:** unchanged, but check it stays fast for a 2-hour flight. Positions are already thinned, so no extra simplification should be needed; measure it.
- **`GET /api/history/tracks?ids=1,2,3`:** several flights' positions in one request for the map overlay. Use compact columns (parallel arrays `t[]`, `lat[]`, `lon[]`, `alt[]`), not objects, with at most 20 ids.

## UI

### Search

- A **search box in the header**, available on the map page in both live and playback mode. Typing runs `/api/history/search`, debounced at about 250 ms, and shows a dropdown with up to 20 results: ICAO, last callsign, other callsigns, country, "live" or the last-seen time, and the flight count. Arrow keys and Enter work; Esc closes it. **`/` focuses the box**, unless you're typing in an input.
- **Picking a result:**
  - **live aircraft:** select it, as a table click does (pan if it has a position), and open its History section
  - **aircraft not received now:** open the detail panel in a **history-only state** for that ICAO, with no live fields and the badge "Not received now · last seen <time>". Its most recent flight's track is shown on the map, and the map fits that track.
- When history is disabled, the box only searches the live table on the client side (ICAO or callsign), with no dropdown of past aircraft.

### History section in the detail panel

The detail panel gets a **History** section, collapsible and remembered with `useStored`, for whatever is selected: from search, a table click or a map click.

- **Summary:** first seen ever, last seen, total flights, days seen, callsigns with counts, max altitude, closest and furthest distance. Add "Details cover flights since <retainedFrom>" when older flights have been deleted.
- **Flights list:** newest first, loaded in pages of 20 with a "Load more" button that uses `before`. Each row shows:
  - the local date and time, the callsign, the duration, max altitude, closest/furthest distance, and an ADS-B or Mode-S marker
  - a **track toggle** (eye icon) to show or hide that flight on the map
  - **Play**, which seeks the tape to `first_seen − 30s` in playback and keeps the aircraft selected; disabled with a tooltip when `onTape` is false
  - **Profile**, which opens the flight profile (below)

  The open flight is highlighted.
- **"Show last 10 on map"** turns on the tracks of the 10 newest flights. **"Clear tracks"** removes every historical track.

### Historical tracks on the map

- **`HistoryTrackLayer`:** drawn **above** waypoints and the coverage layers, and **below** live aircraft and live trails.
  - Use the same altitude colors as live trails, but **dashed and thinner**, so they never look like live traffic.
  - Draw a small start dot and end dot, with the time as a tooltip.
  - Hovering anywhere on a track shows that point's time, altitude, ground speed and track, so you can see where the aircraft was when.
- **Fit to tracks:** the first time tracks are turned on for an aircraft, fit the map to them once (not on every toggle). A "Fit" button in the History section does it again on demand.
- **At most 20 tracks at a time.** At the limit, the toggle explains why it's disabled.
- **Track data is cached per flight id** for the session, so toggling doesn't refetch. Tracks are fetched in batches with `/api/history/tracks`.
- Tracks stay visible across live/playback switches until cleared, or until a different aircraft is selected. Selecting another aircraft clears them (one aircraft's history at a time).

### Flight profile

- A small **ECharts** chart in the panel for the open flight: altitude over time, plus ground speed on a second axis. For Mode-S-only flights, show the altitude samples, with squawk changes marked. Lazy-load ECharts the same way as the Statistics page, so the map bundle doesn't grow; report the chunk sizes.
- **Hovering the chart marks the matching point on the map track,** and hovering the track marks the chart. One shared hover state.

### URL and sharing

- Add `history=<icao>` and `flights=<id,id,…>` (the visible tracks) to `state/url.ts`. A link restores the selection, or the history-only panel for a non-live aircraft, plus the shown tracks and the open profile. It combines with `t`/`speed` from playback.
- "Copy link" includes them.

## Privacy rule

**Never put a real receiver position in code, tests, fixtures, docs or examples.** Distances in the UI come from the configured receiver at run time. Fixtures and tests use fictional aircraft (made-up ICAO addresses and callsigns) and fictional positions around the placeholder receiver. README screenshots come from `make demo`.

## Tests

- **Backend:**
  - the `aircraft_seen` column migration and backfill, the update on flight close, that it survives `history.retention`, and that it's still pruned by `stats.retention`
  - search: hex-prefix vs callsign prefix and substring, one result per ICAO, live first, the limit
  - the summary aggregates and `retainedFrom`
  - flight paging with `before`, with no duplicates or gaps across pages
  - `onTape` against a fake tape extent
  - `/tracks` with the id limit and the compact format
  - 503 when history is disabled
  - contract fixtures for every new JSON shape
  - a benchmark of `/flights/{id}` for a 2-hour flight
- **UI** (Vitest):
  - the search box: debounce, keyboard navigation, the `/` shortcut ignored in inputs, the live vs non-live result paths
  - the history-only panel state
  - the flights list paging
  - track toggles, the 20-track limit, fit-once behavior, tracks cleared when another aircraft is selected
  - the shared hover state between profile and track
  - the URL round trip of `history`/`flights` combined with `t`/`speed`
  - everything hidden when history is disabled
- `go test -race ./...`, `go vet`, `gofmt`, `go fix`, `npm test`, `npm run lint`, `npm run typecheck` all clean. Report the map and lazy chunk sizes.

## Milestones

1. **Backend:** the `aircraft_seen` extension (migration with backfill, update on close), the callsign index, `/api/history/search`, `/api/history/aircraft/{icao}`, paging and the new fields on `/api/history/flights`, `/api/history/tracks`, fixtures and tests.
2. **Search and selection:** the header search box with the dropdown and keyboard, live vs history-only selection, the history-only panel state, URL `history=`.
3. **History section and tracks:** summary, the paged flights list, `HistoryTrackLayer` (dashed altitude tracks, start/end, hover info), fit, the 20-track limit, Play into playback, URL `flights=`.
4. **Profile and docs:** the lazy ECharts flight profile with the shared hover, Mode-S samples, the README section ("Aircraft history": search, panel, tracks, playback link), and `CLAUDE.md` notes.
