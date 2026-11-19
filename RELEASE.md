# Release notes

- 2026-11-19: M1 — YAML config with validation, AVR TCP reader with reconnect, file replay, CRC-24 with optional single-bit fix, DF17/18 TC1–4 callsign decoding.
- 2026-11-19: M2 — Altitude (25 ft and Gillham), global/local CPR with exact NL, range and speed sanity checks; file replay uses capture-time timestamps.
- 2026-11-19: M3 — TC19 velocity (all subtypes, vertical rate, GNSS-baro diff), TC28 emergency state and squawk, TC31 ADS-B version; emergency changes logged.
- 2026-11-19: M4 — DF11 all-call, DF0/4/5/16/20/21 via address/parity with known-address table, altitude/squawk/on-ground, BDS 2,0 callsign.
- 2026-11-19: M5 — Aircraft tracker with TTL, HTTP API: /api/aircraft (filter, sort), /api/aircraft/{icao}, /api/stats, /api/stream (SSE), /healthz.
- 2026-11-19: M6 — Beast binary input (beast://host:30005): 0x1A unescaping, resync on corruption, 12 MHz timestamps, signal level as RSSI dBFS on aircraft.
- 2026-11-19: M7 — README: features, running against dump1090 (AVR/Beast), offline replay, API reference with sample curl output, architecture overview.
- 2026-11-19: UI M1 — /api/receiver, UI embedded via go:embed with SPA fallback, placeholder web/dist, Makefile, Aircraft JSON contract fixture and Go test.
- 2026-11-19: /api/ws WebSocket (gorilla/websocket) pushes receiver, aircraft snapshots and stats every streamInterval; UI uses it instead of SSE and polling.
- 2026-11-19: UI M2 — Vite/React/TS scaffold in web/, /api/ws client with reconnect backoff, state reducer with trails, header with counts/rates/status, Vitest contract test.
- 2026-11-19: UI M3 — Leaflet/OSM map: receiver, 25/50/100 nm rings, rotated altitude-colored markers updated in place, labels, legend, colored trails, stale fading.
- 2026-11-19: UI M4 — Sortable/filterable aircraft table (TanStack v9), Mode-S-only rows, pinned emergencies, map/table selection sync, detail panel, resizable split layout.
