# TODO

Candidate improvements, roughly by value. Move items to RELEASE.md when done.

## Data quality

- [ ] **Full ICAO country table:** about 200 allocations from ICAO Annex 10 Vol III; `internal/tracker/country.go`
      has 41.

## Web UI

- [ ] **Shareable links:** `/?icao=738ABC` selects the aircraft on load; filters are kept in the URL.

## Operations

- [ ] **Prometheus `/metrics`:** existing counters (frames, CRC failures, messages/s, aircraft) in text exposition
      format, stdlib only.
- [ ] **History:** keep a few hours of tracks, or log flights to CSV/SQLite (SQLite adds a dependency, so ask
      first).

## Housekeeping

- [ ] **CI:** GitHub Actions running `make lint test` on push and pull requests.
