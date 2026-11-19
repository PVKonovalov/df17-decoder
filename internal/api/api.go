package api

import (
	"cmp"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/source"
	"github.com/PVKonovalov/df17-decoder/internal/stats"
	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

// HealthWindow is how recently the source must have delivered a frame for /healthz to report healthy.
const HealthWindow = 30 * time.Second

// Deps are the read-only views the API serves.
type Deps struct {
	Tracker        *tracker.Tracker
	Stats          *stats.Stats
	Source         func() source.StatsSnapshot
	SourceURL      string
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
	s := &server{d}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/aircraft", s.aircraftList)
	mux.HandleFunc("GET /api/aircraft/{icao}", s.aircraftOne)
	mux.HandleFunc("GET /api/stats", s.stats)
	mux.HandleFunc("GET /api/stream", s.stream)
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint: "+r.Method+" "+r.URL.Path)
	})
	return mux
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
	Aircraft           int            `json:"aircraft"`
}

func (s *server) stats(w http.ResponseWriter, _ *http.Request) {
	now := s.Now()
	st := s.Stats.Snapshot(now)
	src := s.Source()
	resp := statsResponse{
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
		Aircraft:           len(s.Tracker.Snapshot()),
	}
	if !src.LastFrame.IsZero() {
		resp.Source.LastFrame = &src.LastFrame
	}
	writeJSON(w, http.StatusOK, resp)
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
		list := s.Tracker.Snapshot()
		slices.SortFunc(list, func(a, b tracker.Aircraft) int { return cmp.Compare(a.ICAO, b.ICAO) })
		data, err := json.Marshal(list)
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

func (s *server) healthz(w http.ResponseWriter, _ *http.Request) {
	last := s.Source().LastFrame
	if last.IsZero() || s.Now().Sub(last) > HealthWindow {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "no frames from source in the last 30s"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
