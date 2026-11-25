package api

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/coverage"
	"github.com/PVKonovalov/df17-decoder/internal/geo"
	"github.com/PVKonovalov/df17-decoder/internal/history"
	"github.com/PVKonovalov/df17-decoder/internal/playback"
	"github.com/PVKonovalov/df17-decoder/internal/source"
	"github.com/PVKonovalov/df17-decoder/internal/stats"
	"github.com/PVKonovalov/df17-decoder/internal/storage"
	"github.com/PVKonovalov/df17-decoder/internal/tape"
	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

// HealthWindow is how recently the source must have delivered a frame for /healthz to report healthy.
const HealthWindow = 30 * time.Second

// Receiver is the receiver position served on /api/receiver.
type Receiver struct {
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`
	MaxRangeNm float64 `json:"maxRangeNm"`
}

// MapConfig is the web UI tile layer served on /api/map and in the "map" WebSocket message.
type MapConfig struct {
	TileURL              string     `json:"tileUrl"`
	Attribution          string     `json:"attribution"`
	MaxZoom              int        `json:"maxZoom"`
	DarkFilter           bool       `json:"darkFilter"`
	WaypointLabelMinZoom int        `json:"waypointLabelMinZoom"`
	Waypoints            []Waypoint `json:"waypoints"`
}

// Waypoint is a configured fix: decimal degrees, plus the AIP DMS text for the tooltip.
type Waypoint struct {
	Name   string  `json:"name"`
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
	LatDMS string  `json:"latDms"`
	LonDMS string  `json:"lonDms"`
	Icon   string  `json:"icon"`
	Color  string  `json:"color,omitempty"`
	Note   string  `json:"note,omitempty"`
}

// Deps are the read-only views the API serves.
type Deps struct {
	Receiver Receiver
	Map      MapConfig
	UI       fs.FS // built web UI served at /; nil disables it
	Tracker  *tracker.Tracker
	Coverage *coverage.Coverage // range outline; nil serves an empty one
	History  *history.Store     // flight history; nil when disabled
	Tape     *tape.Recorder     // raw frame recorder; nil when disabled
	TapeData *tape.Tape         // the recording read back; nil when the tape is disabled
	Playback *playback.Manager  // tape playback over /api/ws; nil when disabled
	Storage  *storage.Reporter  // disk usage for /api/storage
	// CoverageInterval is how often /api/ws pushes the outline; 10 s when zero.
	CoverageInterval time.Duration
	// GridInterval is how often /api/ws pushes the coverage grid; 60 s when zero.
	GridInterval   time.Duration
	Stats          *stats.Stats
	Source         func() source.StatsSnapshot
	SourceURL      string
	Version        string // program version reported in stats
	StreamInterval time.Duration
	Logger         *slog.Logger
	Now            func() time.Time // wall clock; time.Now when nil
}

type server struct{ Deps }

// New returns the HTTP handler with all routes.
func New(d Deps) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	if d.Coverage == nil {
		d.Coverage = coverage.New(geo.Point{Lat: d.Receiver.Lat, Lon: d.Receiver.Lon})
	}
	if d.Map.Waypoints == nil {
		d.Map.Waypoints = []Waypoint{}
	}
	if d.CoverageInterval <= 0 {
		d.CoverageInterval = defaultCoverageInterval
	}
	if d.GridInterval <= 0 {
		d.GridInterval = defaultGridInterval
	}
	s := &server{d}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/aircraft", s.aircraftList)
	mux.HandleFunc("GET /api/aircraft/{icao}", s.aircraftOne)
	mux.HandleFunc("GET /api/stats", s.stats)
	mux.HandleFunc("GET /api/stream", s.stream)
	mux.HandleFunc("GET /api/ws", s.ws)
	mux.HandleFunc("GET /api/history/flights", s.historyFlights)
	mux.HandleFunc("GET /api/history/flights/{id}", s.historyFlight)
	mux.HandleFunc("GET /api/history/at", s.historyAt)
	mux.HandleFunc("GET /api/history/timeline", s.historyTimeline)
	mux.HandleFunc("GET /api/reference/aircraft/{icao}", s.aircraftInfo)
	mux.HandleFunc("PUT /api/reference/aircraft/{icao}", s.putAircraftInfo)
	mux.HandleFunc("DELETE /api/reference/aircraft/{icao}", s.deleteAircraftInfo)
	mux.HandleFunc("GET /api/reference/routes/{callsign}", s.route)
	mux.HandleFunc("PUT /api/reference/routes/{callsign}", s.putRoute)
	mux.HandleFunc("DELETE /api/reference/routes/{callsign}", s.deleteRoute)
	mux.HandleFunc("GET /api/storage", s.storage)
	mux.HandleFunc("GET /api/stats/hourly", s.statsHourly)
	mux.HandleFunc("GET /api/stats/countries", s.statsCountries)
	mux.HandleFunc("GET /api/stats/operators", s.statsOperators)
	mux.HandleFunc("GET /api/stats/range-daily", s.statsRangeDaily)
	mux.HandleFunc("GET /api/stats/altitude", s.statsAltitude)
	mux.HandleFunc("GET /api/stats/notable", s.statsNotable)
	mux.HandleFunc("GET /api/tape/extent", s.tapeExtent)
	mux.HandleFunc("GET /api/receiver", s.receiver)
	mux.HandleFunc("GET /api/map", s.mapConfig)
	mux.HandleFunc("GET /api/coverage", s.coverage)
	mux.HandleFunc("GET /api/coverage/grid", s.coverageGrid)
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint: "+r.Method+" "+r.URL.Path)
	})
	mux.HandleFunc("/", s.ui)
	return mux
}

func (s *server) receiver(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Receiver)
}

func (s *server) mapConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Map)
}

// coverage serves GET /api/coverage: the range outline, furthest position per bearing bucket.
func (s *server) coverage(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Coverage.Snapshot())
}

// coverageGrid serves GET /api/coverage/grid: positions and signal levels per bearing bucket, ring and altitude band.
func (s *server) coverageGrid(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Coverage.Grid())
}

// ui serves files of the embedded UI; GET paths that are not files fall back to index.html (SPA routing).
// Vite's hashed assets/ files are cached for a year, everything else is revalidated.
func (s *server) ui(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.UI == nil {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if fi, err := fs.Stat(s.UI, name); name == "" || err != nil || fi.IsDir() {
		name = "index.html"
	}
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFileFS(w, r, s.UI, name)
}

// aircraftList serves GET /api/aircraft[?withPosition=true][&sort=distance|altitude|lastSeen].
// Default order is by ICAO address; distance ascending, altitude descending, lastSeen most recent first;
// aircraft without the sort value come last.
func (s *server) aircraftList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	withPosition := false
	if v := q.Get("withPosition"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "withPosition must be true or false")
			return
		}
		withPosition = b
	}

	list := s.Tracker.Snapshot()
	if withPosition {
		list = slices.DeleteFunc(list, func(a tracker.Aircraft) bool { return !a.HasPosition })
	}

	byICAO := func(a, b tracker.Aircraft) int { return cmp.Compare(a.ICAO, b.ICAO) }
	switch sortBy := q.Get("sort"); sortBy {
	case "":
		slices.SortFunc(list, byICAO)
	case "distance":
		slices.SortFunc(list, func(a, b tracker.Aircraft) int {
			return cmp.Or(nilsLast(a.DistanceNm, b.DistanceNm, false), byICAO(a, b))
		})
	case "altitude":
		slices.SortFunc(list, func(a, b tracker.Aircraft) int {
			return cmp.Or(nilsLast(a.AltBaro, b.AltBaro, true), byICAO(a, b))
		})
	case "lastSeen":
		slices.SortFunc(list, func(a, b tracker.Aircraft) int {
			return cmp.Or(b.LastSeen.Compare(a.LastSeen), byICAO(a, b))
		})
	default:
		writeError(w, http.StatusBadRequest, "sort must be distance, altitude or lastSeen")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// nilsLast compares two optional values, nil after any value; desc reverses the order of non-nil values.
func nilsLast[T cmp.Ordered](a, b *T, desc bool) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	case desc:
		return cmp.Compare(*b, *a)
	default:
		return cmp.Compare(*a, *b)
	}
}

func (s *server) aircraftOne(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("icao")
	icao, err := strconv.ParseUint(raw, 16, 32)
	if err != nil || len(raw) != 6 {
		writeError(w, http.StatusBadRequest, "icao must be 6 hex digits")
		return
	}
	a, ok := s.Tracker.Get(uint32(icao))
	if !ok {
		writeError(w, http.StatusNotFound, "aircraft not found")
		return
	}
	writeJSON(w, http.StatusOK, a)
}

type sourceStatus struct {
	URL            string     `json:"url"`
	Connected      bool       `json:"connected"`
	Reconnects     uint64     `json:"reconnects"`
	MalformedLines uint64     `json:"malformedLines"`
	LastFrame      *time.Time `json:"lastFrame,omitempty"`
}

type statsResponse struct {
	Version            string         `json:"version"`
	ServerTime         time.Time      `json:"serverTime"` // the clock the other times compare with
	UptimeSeconds      float64        `json:"uptimeSeconds"`
	Source             sourceStatus   `json:"source"`
	Frames             uint64         `json:"frames"`
	FramesPerDF        map[int]uint64 `json:"framesPerDf"`
	Messages           uint64         `json:"messages"`
	MessagesPerSecond  float64        `json:"messagesPerSecond"`
	CRCFailures        uint64         `json:"crcFailures"`
	FixedBitErrors     uint64         `json:"fixedBitErrors"`
	Unsupported        uint64         `json:"unsupported"`
	Invalid            uint64         `json:"invalid"`
	AddressParityDrops uint64         `json:"addressParityDrops"`
	Positions          uint64         `json:"positions"`
	CPRRejected        uint64         `json:"cprRejected"`
	NoPosition         uint64         `json:"noPosition"`
	CommBAmbiguous     uint64         `json:"commBAmbiguous"`
	CommBRejected      uint64         `json:"commBRejected"`
	Aircraft           int            `json:"aircraft"`
	*historyStats
	*tapeStats
}

// tapeStats are in statsResponse only when the tape is enabled.
type tapeStats struct {
	TapeRecording     bool       `json:"tapeRecording"`
	TapePaused        bool       `json:"tapePaused"` // free disk space below tape.minFreeMB
	TapeOldest        *time.Time `json:"tapeOldest,omitempty"`
	TapeNewest        *time.Time `json:"tapeNewest,omitempty"`
	TapeBytes         int64      `json:"tapeBytes"`
	TapeFramesWritten uint64     `json:"tapeFramesWritten"`
	TapeDropped       uint64     `json:"tapeDropped"`
	TapeWriteErrors   uint64     `json:"tapeWriteErrors"`
	TapeLastFlush     *time.Time `json:"tapeLastFlush,omitempty"`
	PlaybackSessions  *int       `json:"playbackSessions,omitempty"` // when playback is enabled
}

// tapeExtent serves GET /api/tape/extent: the oldest and newest recorded times and the recorded spans (gaps over
// a minute split them), or 503 when the tape is disabled.
func (s *server) tapeExtent(w http.ResponseWriter, _ *http.Request) {
	if s.TapeData == nil {
		writeError(w, http.StatusServiceUnavailable, "the tape is disabled (tape.enabled)")
		return
	}
	writeJSON(w, http.StatusOK, s.TapeData.Extent())
}

// optTime returns &t, or nil for the zero time.
func optTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// historyStats are in statsResponse only when history is enabled.
type historyStats struct {
	HistoryFlightsOpen   int64      `json:"historyFlightsOpen"`
	HistoryPointsWritten uint64     `json:"historyPointsWritten"`
	HistoryDropped       uint64     `json:"historyDropped"`
	HistoryWriteErrors   uint64     `json:"historyWriteErrors"`
	HistoryLastFlush     *time.Time `json:"historyLastFlush,omitempty"`
}

func (s *server) stats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.statsSnapshot())
}

func (s *server) statsSnapshot() statsResponse {
	now := s.Now()
	st := s.Stats.Snapshot(now)
	src := s.Source()
	resp := statsResponse{
		Version:       s.Version,
		ServerTime:    now,
		UptimeSeconds: math.Round(st.Uptime.Seconds()),
		Source: sourceStatus{
			URL:            s.SourceURL,
			Connected:      src.Connected,
			Reconnects:     src.Reconnects,
			MalformedLines: src.Malformed,
		},
		Frames:             st.Frames,
		FramesPerDF:        st.FramesPerDF,
		Messages:           st.Messages,
		MessagesPerSecond:  math.Round(st.MessagesPerSecond*100) / 100,
		CRCFailures:        st.CRCFailures,
		FixedBitErrors:     st.FixedBitErrors,
		Unsupported:        st.Unsupported,
		Invalid:            st.Invalid,
		AddressParityDrops: st.AddressParityDrops,
		Positions:          st.Positions,
		CPRRejected:        st.CPRRejected,
		NoPosition:         st.NoPosition,
		CommBAmbiguous:     st.CommBAmbiguous,
		CommBRejected:      st.CommBRejected,
		Aircraft:           s.Tracker.Len(),
	}
	if !src.LastFrame.IsZero() {
		resp.Source.LastFrame = &src.LastFrame
	}
	if s.History != nil {
		h := s.History.Stats()
		resp.historyStats = &historyStats{
			HistoryFlightsOpen:   h.FlightsOpen,
			HistoryPointsWritten: h.PointsWritten,
			HistoryDropped:       h.Dropped,
			HistoryWriteErrors:   h.WriteErrors,
		}
		if !h.LastFlush.IsZero() {
			resp.HistoryLastFlush = &h.LastFlush
		}
	}
	if s.Tape != nil {
		t := s.Tape.Stats()
		resp.tapeStats = &tapeStats{
			TapeRecording: t.Recording, TapePaused: t.Paused,
			TapeOldest: optTime(t.Oldest), TapeNewest: optTime(t.Newest), TapeBytes: t.Bytes,
			TapeFramesWritten: t.FramesWritten, TapeDropped: t.Dropped, TapeWriteErrors: t.WriteErrors,
			TapeLastFlush: optTime(t.LastFlush),
		}
		if s.Playback != nil {
			resp.PlaybackSessions = new(s.Playback.Active())
		}
	}
	return resp
}

// aircraftByICAO returns the current aircraft sorted by ICAO address.
func (s *server) aircraftByICAO() []tracker.Aircraft { return sortedAircraft(s.Tracker) }

func sortedAircraft(tr *tracker.Tracker) []tracker.Aircraft {
	list := tr.Snapshot()
	slices.SortFunc(list, func(a, b tracker.Aircraft) int { return cmp.Compare(a.ICAO, b.ICAO) })
	return list
}

// stream serves Server-Sent Events: the full aircraft list (sorted by ICAO) immediately and then every
// StreamInterval, until the client disconnects or the server shuts down.
func (s *server) stream(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ticker := time.NewTicker(s.StreamInterval)
	defer ticker.Stop()
	for {
		data, err := json.Marshal(s.aircraftByICAO())
		if err != nil {
			s.Logger.Error("stream: marshal", "err", err)
			return
		}
		if _, err = fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return
		}
		if err = rc.Flush(); err != nil {
			return
		}
		select {
		case <-ticker.C:
		case <-r.Context().Done():
			return
		}
	}
}

// healthz serves GET /healthz: 200 while frames arrive, and while the source stays connected after delivering
// frames (a quiet sky sends nothing); 503 before the first frame, or when the source is down and silent.
func (s *server) healthz(w http.ResponseWriter, _ *http.Request) {
	src := s.Source()
	switch {
	case src.LastFrame.IsZero():
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "no frames from the source yet"})
	case s.Now().Sub(src.LastFrame) <= HealthWindow:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	case src.Connected:
		writeJSON(w, http.StatusOK, map[string]string{"status": "idle: connected, no frames in the last 30s"})
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "source disconnected, no frames in the last 30s"})
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
