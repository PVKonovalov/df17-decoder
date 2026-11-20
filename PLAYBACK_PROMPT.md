# Task: "Tape recorder": raw frame recording 24/7 and faithful playback in the UI

## Context

Read `CLAUDE.md` first and follow its rules: ask before each milestone, add a line to `RELEASE.md` after each one, don't commit or push, run `go fix ./...`, keep comments sparse.

The service already keeps server-side trails (`trails.window`) and an optional SQLite flight history (`internal/history`: flights, thinned positions, Mode-S samples, `/api/history/*`). SQLite is good for search and statistics, but it can't replay what the UI showed: it has no RSSI, Comm-B, selected altitude, squawk timeline for ADS-B aircraft, or coverage.

This task adds a **tape recorder**:

1. **The tape.** Every raw frame the service receives is recorded 24/7 to rotating, compressed segment files.
2. **The player.** A playback session per WebSocket connection runs a *second, separate* decoder pipeline (source → tracker) over the tape. It produces exactly the messages the live feed produces, so the map, table, detail panel and trails work unchanged.
3. **The deck.** A tape-deck UI: transport controls, a tape counter, a timeline, a REC indicator, and links to a moment.
4. **SQLite as the tape index.** A traffic-density strip, event markers, and "find a flight → wind the tape to it".

## Decisions already made

- **Raw tape + SQLite index.** The tape is the source of truth for replay; SQLite is the index for search and timeline statistics.
- **Segments of 1 hour, kept 14 days by default** (about 0.5 GB at the current ~55 frames/s; both are configurable).
- **The recorder runs 24/7 in real time,** whether or not anyone is watching. It's independent of playback and of `history.enabled`.
- Playback starts with this task. No in-memory-only "instant replay" mode is needed: the tape covers it.
- **No new dependencies.** Use `compress/gzip` from the stdlib.

## The tape (`internal/tape`, new)

### What is recorded

- Tap the frame stream in `app.Service`, so every `source.Frame` is recorded **before decoding**, including frames the decoder later rejects. Recording must never block or slow ingest: use a bounded channel to a writer goroutine, and drop and count when it's full.
- Beast timestamps are 12 MHz receiver-clock counters. They reset when dump1090 restarts, so they can't be used to seek by wall time. **Each record therefore stores the frame's receive time** (`Frame.Time`, unix nanoseconds) next to the raw data.
- **Record format (v1)**, inside a gzip stream:
  - The file starts with a header: the magic bytes `DF17TAPE`, a version byte, and the segment start time.
  - Then one record per frame:

    | Field | Size |
    | --- | --- |
    | time delta since the previous record, ns | unsigned varint |
    | 12 MHz timestamp (0 if none) | 6 bytes |
    | signal byte as received (0 = none) | 1 byte |
    | frame length | 1 byte |
    | frame bytes | 7 or 14 |

  - The format is source-agnostic: AVR input records the same way, with signal 0. Keep the raw signal byte so RSSI replays exactly.

### Segments, durability and retention

- **Segments:** one file per `tape.segment` (default 1h), aligned to UTC boundaries. Name them `<dir>/YYYY/MM/DD/YYYYMMDDTHH0000Z.tape.gz`, so listing and retention are simple string operations.
- **Durability:**
  - Call `gzip.Writer.Flush()` every `tape.flushInterval` (default 5s), so a crash loses at most that much.
  - Write the active segment as `….tape.gz.part` and rename it when the segment closes.
  - At startup, finalize any leftover `.part` file: keep it, it's readable up to its last flush. Readers must tolerate a truncated gzip tail (`io.ErrUnexpectedEOF`): use every complete record and stop quietly.
- **Retention:** once an hour, delete segments older than `tape.retention` (default 336h) and remove empty day directories.
- **Disk guard:** if free space in `tape.dir` falls below `tape.minFreeMB` (default 1024), delete the oldest segments first. If that's not enough, pause recording with a warning, and resume when space returns. Never fill the disk.
- **Stats in `/api/stats`:** `tapeRecording` (bool), `tapeOldest`, `tapeNewest`, `tapeBytes` (total on disk), `tapeFramesWritten`, `tapeDropped`, `tapeWriteErrors`, `tapeLastFlush`. Log errors once per failure streak and log recovery, as `saveCoverage` does.
- **Gaps:** service downtime or dump1090 outages leave holes in the tape. The reader reports them; nothing fills them.

### Reader

- `tape.Open(dir).Seek(t)` returns an iterator of `source.Frame`, starting at the first record at or after `t` and continuing across segments, with gaps reported.
- Seeking within a segment means decompressing from its start and skipping records. An hour is about 5 MB raw, so this takes milliseconds. **Benchmark it**, and add a sparse per-segment index only if the benchmark shows it's needed.
- **`GET /api/tape/extent`:** the oldest and newest recorded time, plus a list of recorded spans with gaps of more than 60s merged out, for the timeline.
- **Optional:** a `tape://<dir>?from=<RFC3339>&speed=<x>` source for `source.url`, so offline development can replay a real recorded day through the whole service. Reuse the existing replay clock.

## The player (`internal/playback`, new)

### Session

- One session per WebSocket connection that enters playback. A session owns its own `tracker.Tracker` (same resolver and options as live) and a tape reader. The trackers are completely separate.
- **No side effects:** a session's tracker gets **no** `Flights` sink, **no** `Coverage` recorder and its own `stats.Stats`. Playback must never write to SQLite, the live coverage, or live stats.
- **Virtual clock.** The tracker currently ages entries with `time.Now` (`Tracker.wall`). Make the clock an `Options` field (default `time.Now`). The session supplies a virtual clock that advances only while playing, scaled by speed. **Pausing must not expire aircraft or trails.**
- **Seeking to `T`:**
  1. Create a fresh tracker.
  2. Feed it frames from `T − preroll` at full speed, with no pacing and no output. Use `preroll = max(trails.window, tracker.ttl) + 1m`, so trails and aircraft at `T` look exactly as they did live.
  3. Set the virtual clock to `T`, send the full state, and continue paced.
- **Rewind** (⏪) is a seek to an earlier time (−10s, −1m, or a timeline click). There's no reverse decoding.
- **Speed:** 1×, 2×, 4×, 8×, 16× and 60×. Pacing follows the recorded frame times divided by the speed.
- **End of tape:** when playback catches up with "now" (within one `api.streamInterval`), the session ends and the connection switches back to live automatically.
- **Gaps:** when the next frame is more than 60s away, jump over the gap, and tell the UI so the counter visibly skips.
- **Limits:** at most `playback.maxSessions` (default 4) concurrent sessions, refused with a clear error. A session paused for longer than `playback.idleTimeout` (default 30m) is freed, and the next play re-seeks. A session ends when its WebSocket closes or the service context ends.
- **CPU check:** a 60× session is about 3,300 frames/s, which is trivial for the decoder. Measure it in a benchmark anyway.

### WebSocket protocol (`internal/api/ws.go`)

- The UI keeps a single WebSocket. Today the server only pushes and the read loop just drains. Add **client→server control messages**: `{type:"playback", data:{action, t?, speed?}}`, with `action` one of `play`, `pause`, `seek`, `speed`, `step` (±seconds) or `live`.
- In playback, the connection's `aircraft`, `trails` and `trailPoints` messages come from the session, not the live tracker:
  - after every seek, a full `trails` message
  - after that, deltas only, exactly as in live
- `stats` stays live (header and REC indicator). Add a `playback` message pushed with every update: `{mode:"live"|"playback", t, speed, playing, gapSkipped?, tapeStart, tapeEnd}`. That's the tape counter's source.
- `receiver`, `map` and coverage messages are unchanged. Coverage stays live and is not replayed in this task.
- Validate every control message: unknown actions and out-of-range times get an error message back, never a panic or a dropped connection.
- Update `api/types.ts`, `toAction` (unknown types still ignored), and the contract tests, as `CLAUDE.md` requires for API changes.

## The deck (UI, `web/src`)

- **Transport bar** under the map header. In live mode it shows only **● REC** and a ⏮/⏪ button to enter playback. In playback:
  - ⏮ oldest recording
  - ⏪ −10s (Shift: −1m)
  - ⏯ play/pause
  - ⏩ cycles the speed
  - ⏭ **LIVE**
- **Tape counter:** the playback time (local time, with the date when it isn't today). Click it to type a date and time. In live mode it shows "LIVE".
- **Timeline:**
  - recorded spans from `/api/tape/extent`, with gaps hatched
  - a playhead you can drag or click to seek
  - a zoom between 1 hour, 6 hours, 1 day and the whole tape
- **When `history.enabled`,** the timeline also shows:
  - a traffic-density strip (aircraft per minute, from the new endpoint below)
  - event markers for emergencies (7500/7600/7700)
- **● REC indicator:**
  - red and steady while `tapeRecording` is true and the last flush is recent
  - amber with a tooltip when frames are dropped, writes fail, or recording is paused because the disk is low
  - grey when the tape is disabled
- **Playback look:** a clear "PLAYBACK" badge with the speed. The map and table keep working: selection, the detail panel, sorting and filters. The "new aircraft" highlight uses data time, so it works in playback too.
- **Moment links:** extend `state/url.ts` with `t` (RFC 3339) and `speed`. Opening such a link starts in playback at that moment, combined with `icao` for "this aircraft at this moment". "Copy link" in the detail panel includes `t` when in playback.
- **Keyboard** (not while typing in an input):
  - Space: play/pause
  - ←/→: ±10s
  - Shift+←/→: ±1m
  - L: live
- **Mode-S-only rows** behave as in live.

## SQLite as the tape index

- **`GET /api/history/timeline?from=&to=&bucket=1m`:** per bucket, the number of flights active, split into ADS-B and Mode-S-only, plus events (emergency squawks, with flight id and ICAO). Cap the bucket count; this feeds the density strip.
- **Find a flight:** a small search box in the deck (callsign or ICAO, using `/api/history/flights`). Picking a result seeks to its `first_seen − 30s` and selects that aircraft.
- With `history.enabled: false`, the deck still works from the tape alone; the strip, events and search are hidden.

## Config

Add to `internal/config` (`Default()`, validation, and `config/config.example.yaml`, which a test requires to equal `Default()`), using camelCase keys:

```yaml
tape:
  # Record every received frame to compressed hourly segments for playback.
  enabled: true
  # Relative to the working directory (the state dir under systemd); Docker: /var/lib/df17-decoder/tape
  # Must be writable when enabled, or the service refuses to start (exit code 2).
  dir: tape
  segment: 1h
  # Crash safety: at most this much recording is lost on a crash.
  flushInterval: 5s
  # Segments older than this are deleted hourly; 0 keeps everything (the disk guard still applies).
  retention: 336h
  # The oldest segments are deleted, then recording pauses, when free space drops below this.
  minFreeMB: 1024

playback:
  maxSessions: 4
  idleTimeout: 30m
```

Validation: `segment` must divide a day evenly (for example 10m, 15m, 30m, 1h, 2h); `flushInterval` must be shorter than `segment`.

## Privacy rule

**Never put a real receiver position in code, tests, fixtures, docs or examples.** Use the placeholder coordinates from `config/config.example.yaml`, or values from the configured receiver at run time. Add `tape/` and `*.tape.gz*` to `.gitignore` and `.dockerignore`: a tape reveals where the receiver is, just like `coverage.json` and `history.db`.

## Tests

- **Recorder:**
  - a round trip (frames in → the same `Data`, `Time` to the nanosecond, `Timestamp` and signal out)
  - rotation exactly at the UTC boundary (with an injected clock)
  - `.part` finalization
  - a truncated gzip tail readable up to the last flush
  - retention
  - the disk guard (with injected free-space and size functions)
  - a full channel drops and counts without blocking ingest
- **Reader:** seeking to the start, middle and end of a segment; across segment boundaries; over a gap; the extent API with merged gaps.
- **Equivalence (key test):** record `testdata/capture.txt` (and the `make busy` capture) through the live pipeline with an injected clock, then seek a playback session to several times `T`. The session's aircraft snapshot and trails at `T` must equal the live tracker's at `T`, ignoring wall-clock-only fields. This proves preroll and the virtual clock are right.
- **Virtual clock:** pausing for longer than `tracker.ttl` expires nothing; 16× expires aircraft 16× faster in wall time.
- **Sessions:** limits, idle timeout, auto-switch to live at the end of the tape, no writes to history, coverage or live stats.
- **WebSocket:** control messages switch the feed; after a seek, a full `trails` then deltas; invalid actions return errors; the contract tests are updated.
- **UI** (Vitest):
  - reducer mode switching
  - `playback` message handling
  - counter formatting (date shown when it isn't today)
  - URL `t`/`speed` round trip with `icao`
  - keyboard shortcuts ignored in inputs
  - REC indicator states
- `go test -race ./...`, `go vet`, `gofmt`, `go fix`, `npm test`, `npm run lint`, `npm run typecheck` all clean. `make dist` stays static with no cgo.

## Milestones

1. **Recorder:** the record format, writer with rotation, flush and `.part` handling, retention, disk guard, stats, the `tape` config section, `.gitignore`/`.dockerignore`, tests. Recording runs 24/7 when enabled.
2. **Reader and virtual clock:** seek across segments, gap reporting, `/api/tape/extent`, the tracker clock option, the optional `tape://` source, benchmarks, and the equivalence test against a live replay.
3. **Playback sessions:** `internal/playback`, the WebSocket control protocol and `playback` message, limits and idle timeout, auto-return to live, the `playback` config section, tests.
4. **The deck:** transport bar, tape counter, timeline with spans and gaps, REC indicator, PLAYBACK badge, keyboard, moment links; tested with `make demo` and a real tape.
5. **SQLite index and docs:**
   - `/api/history/timeline`, the density strip and events, flight search → seek
   - README (tape format, disk use, retention, privacy, playback usage) and `CLAUDE.md` architecture notes
   - systemd and Docker notes: the tape dir in the state dir and the volume
