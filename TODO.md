# TODO

Candidate improvements, roughly by value. Move items to RELEASE.md when done.

## Decoding

- [ ] **Target state and status (TC 29):** selected altitude and heading (autopilot/FMS), shown in the detail panel.
- [ ] **More Comm-B registers:** BDS 4,0 (selected altitude), 5,0 (roll, track, ground speed, TAS), 6,0 (heading,
      IAS, Mach, vertical rate) for Mode-S-only aircraft. Only BDS 2,0 (callsign) is decoded today.

## Data quality

- [ ] **Full ICAO country table:** about 200 allocations from ICAO Annex 10 Vol III; `internal/tracker/country.go`
      has 41.
- [ ] **Beast file replay:** `file://` accepts only AVR text. Replaying a binary capture (`nc host 30005 > capture.bin`)
      would keep RSSI and exact timestamps.

## Web UI

- [ ] **Signal strength:** color or size markers by RSSI (Beast input), or add a signal view.
- [ ] **Coverage plot:** polar outline of the furthest position seen per bearing, for antenna tuning. Needs a small
      backend aggregate (max range per bearing bucket).
- [ ] **Shareable links:** `/?icao=738ABC` selects the aircraft on load; filters are kept in the URL.

## Operations

- [ ] **Prometheus `/metrics`:** existing counters (frames, CRC failures, messages/s, aircraft) in text exposition
      format, stdlib only.
- [ ] **History:** keep a few hours of tracks, or log flights to CSV/SQLite (SQLite adds a dependency, so ask
      first).

## Housekeeping

- [ ] **CI:** GitHub Actions running `make lint test` on push and pull requests.
