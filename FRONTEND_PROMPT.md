# Task: React + TypeScript web UI for `df17-decoder`

## Context

Read `AGENT_PROMPT.md` and `CLAUDE.md` first. This repo is a Go service that decodes Mode S / ADS-B frames from my home receiver (RTL-SDR Blog V4 + dump1090-fa, near Rosh HaAyin, Israel, under the Ben Gurion approach paths). It keeps a live aircraft table and serves it over an HTTP/JSON API:

- `GET /api/aircraft`, `GET /api/aircraft/{icao}`: aircraft state, camelCase JSON. Unknown fields are left out, never sent as zero.
- `GET /api/stream`: Server-Sent Events, a full aircraft snapshot every `api.stream_interval` (1 s by default)
- `GET /api/stats`: uptime, source connection status, frame counters, messages per second, aircraft count
- `GET /healthz`

This task adds a **web UI with a live map of aircraft and a table of their data**, built into the same Go binary. The frontend is the main deliverable, but a few small backend changes are needed (listed below).

If the backend API from `AGENT_PROMPT.md` isn't finished yet, stop and tell me instead of building against an imagined API.

## Backend changes (Go)

1. `GET /api/receiver`: returns `{ "lat", "lon", "maxRangeNm" }` from the config, so the UI can center the map and draw range rings.
2. Serve the built UI at `/`. A small package `web/embed.go` (`package web`) exposes `web/dist` with `//go:embed all:dist`, and `internal/api` serves it. Unknown non-`/api` GET paths fall back to `index.html` (SPA routing). `/api/*` paths that don't exist still return a JSON 404.
3. `go build` must work on a clean checkout without Node: commit a placeholder `web/dist/index.html` that says "UI not built, run `make web`", and `.gitignore` everything else in `web/dist`.
4. **Contract test:** a Go test marshals a fully populated `Aircraft` (every optional field set) and compares the set of JSON keys with the fixture `web/src/api/aircraft.fixture.json`. The same fixture is used in a Vitest test against the TypeScript types (below). Any drift between Go and TS must break a test.
5. No CORS code. In development the Vite proxy handles cross-origin.

## Frontend stack

- `web/`: **Vite + React + TypeScript** (`strict: true`), npm. Commit `package-lock.json`. Set `engines.node` to the current Node LTS.
- Map: **Leaflet + react-leaflet**, OpenStreetMap tiles, no API keys. Show the OSM attribution.
- Table: **TanStack Table** (`@tanstack/react-table`).
- Styling: plain CSS or CSS modules, no UI framework. Support a dark theme (`prefers-color-scheme`), because people often watch planes at night.
- Allowed runtime dependencies: `react`, `react-dom`, `leaflet`, `react-leaflet`, `@tanstack/react-table`. Allowed dev dependencies: `typescript`, `vite`, `@vitejs/plugin-react`, `@types/*`, `vitest`, `@testing-library/react`, `jsdom`, `eslint` with the TypeScript plugin. **Ask me before adding anything else.**

## Data flow

- On load, fetch `/api/receiver` once, then subscribe to `/api/stream` with `EventSource`. Don't poll for aircraft.
- Show the stream state (connected / reconnecting / down) in the header. `EventSource` reconnects by itself; handle the error and open events to update the indicator.
- Poll `/api/stats` every 5 s for the header numbers.
- Keep all aircraft state in one reducer (`useReducer` or a small store hook). Its actions: snapshot received, select aircraft, clear selection, plus trail bookkeeping. The map and table are pure views of that state.
- Write the API types by hand in `web/src/api/types.ts`. They mirror the Go JSON exactly: camelCase, with optional (`?`) fields for everything Go can leave out. Never assume an optional field is present. A real altitude of `0` is valid, so check `!== undefined`, not truthiness.

## Map

- Centered on the receiver, with a receiver marker and dashed range rings at 25, 50 and 100 nm (labelled).
- One marker per aircraft with a position: an airplane SVG in an `L.divIcon`, rotated by `track`, colored by altitude. Use a continuous scale from ground to 40,000 ft and show a legend.
- Label under the marker: callsign (or ICAO hex if there's no callsign), plus altitude in hundreds of feet (for example `WZZ314 040`).
- **Trails:** the client keeps the last ~5 minutes of positions per aircraft, drawn as a polyline with segments in the altitude colors. Trails are client-side only, with no backend change.
- Aircraft whose last position is older than 30 s fade out (lower opacity). They're removed when the backend drops them.
- Clicking a marker selects the aircraft: highlight the marker and its trail, scroll its table row into view, and open a detail panel with all known fields, including a link to the aircraft on an external tracker by ICAO hex.
- Don't re-create markers on every snapshot. Update position and rotation in place so 1 s updates stay smooth with 100+ aircraft.

## Table

- Columns: ICAO, callsign, squawk, altitude (ft), ground speed (kt), track (°), vertical rate (ft/min, with ↑/↓), distance (nm), bearing (°), country, messages, RSSI (only when present), last seen (seconds ago, ticking).
- Every column is sortable. The default sort is distance ascending. Rows with no value sort last.
- **Mode-S-only aircraft** (no position) are listed as well, styled differently (muted, with a "no pos" badge). Clicking them opens the detail panel but doesn't move the map. They're a big part of local light traffic (`4X-` registrations), so don't hide them.
- Rows with emergency squawks 7500/7600/7700, or an emergency state from the backend, are highlighted red and pinned to the top regardless of sort.
- A selected row is highlighted. Clicking a row selects the aircraft and pans the map to it.
- Filters: free text over callsign, ICAO and squawk, plus a "with position only" toggle.

## Layout

- Desktop: map on the left (~65%) and table on the right, with a resizable splitter if it's cheap to build. Narrow screens: map on top, table below.
- Header: aircraft count (total / with position), messages per second, source status (from `/api/stats`), stream status.
- Units: feet, knots, ft/min, nautical miles. Times are relative ("12 s ago").

## Dev and build

- `npm run dev` in `web/`, with a Vite proxy forwarding `/api` to `http://localhost:8080` (SSE must stream through the proxy, not be buffered).
- `npm run build` writes `web/dist`, which `go:embed` picks up.
- Add a `Makefile` (or extend an existing one) with these targets:
  - `web`: `npm ci && npm run build` in `web/`
  - `build`: `web`, then `go build ./cmd/df17-decoder`
  - `test`: `go test -race ./...` and `npm test`
  - `lint`: `go vet ./...`, `npm run lint`, `npm run typecheck`
  - `run`: build and run with `config.yaml`
- `npm run typecheck` (`tsc --noEmit`) and `npm run lint` must be clean.

## Tests

- **Vitest:** altitude→color mapping (boundaries, missing altitude), the reducer (add/update/remove aircraft from snapshots, trail trimming by age, selection that survives snapshots, selection cleared when the aircraft disappears), the table filter and sort (missing values last, emergencies pinned), and the fixture contract test.
- **Go:** the `/api/receiver` handler, SPA fallback vs. JSON 404 for `/api/*`, and the fixture contract test.
- For manual testing without the radio, run the backend with `source.url: file://testdata/capture.txt` and a replay speed of 1.0, so the map moves in real time.

## Milestones

Follow the rules in `CLAUDE.md`: ask for my OK before each milestone, and add a line to `RELEASE.md` after each one.

1. Backend: `/api/receiver`, embed + SPA fallback, placeholder `dist`, Makefile, contract fixture and Go test.
2. Scaffold: Vite/React/TS in `web/`, dev proxy, API types and the Vitest contract test, reducer with the SSE subscription, header with stats and connection status.
3. Map: receiver, range rings, aircraft markers with rotation and altitude colors, labels, legend, trails, fading.
4. Table and interaction: sortable, filterable table, Mode-S-only rows, emergency highlighting, selection synced between map and table, detail panel.
5. Polish: dark theme, responsive layout, performance check with a busy replay (no marker re-creation, no full table re-render on every tick if avoidable), README section with screenshots and dev/prod instructions.
