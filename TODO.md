# TODO

Candidate improvements, roughly by value. Move items to RELEASE.md when done.

## Operations

- [ ] **Prometheus `/metrics`:** existing counters (frames, CRC failures, messages/s, aircraft) in text exposition
      format, stdlib only.
- [ ] **History:** keep a few hours of tracks, or log flights to CSV/SQLite (SQLite adds a dependency, so ask
      first).

## Housekeeping

- [ ] **CI:** GitHub Actions running `make lint test` on push and pull requests.
