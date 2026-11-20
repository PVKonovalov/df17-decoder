package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/playback"
	"github.com/PVKonovalov/df17-decoder/internal/tracker"
	"github.com/gorilla/websocket"
)

const (
	wsWriteTimeout = 10 * time.Second
	wsPingInterval = 30 * time.Second
	wsPongWait     = 2 * wsPingInterval
	wsReadLimit    = 4096

	defaultCoverageInterval = 10 * time.Second
	defaultGridInterval     = time.Minute
)

// wsMessage is one WebSocket text message: Type is "receiver", "map", "trails", "coverage", "coverageGrid",
// "trailPoints", "aircraft", "stats", "playback" or "error".
type wsMessage struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// wsTrails is the "trails" message: every current trail and the window (Go duration) the client trims them to.
type wsTrails struct {
	Window string                          `json:"window"`
	Trails map[string][]tracker.TrackPoint `json:"trails"`
}

// Default CheckOrigin: only same-origin browsers may connect (the Vite dev proxy rewrites Origin).
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 16 * 1024,
	Error: func(w http.ResponseWriter, _ *http.Request, status int, reason error) {
		writeError(w, status, reason.Error())
	},
}

// wsControl is the data of a client "playback" message: action is play, pause, seek (t), speed (speed), step
// (seconds, ± from the playback time) or live.
type wsControl struct {
	Action  string    `json:"action"`
	T       time.Time `json:"t"`
	Speed   float64   `json:"speed"`
	Seconds float64   `json:"seconds"`
}

type wsIncoming struct {
	ctl wsControl
	err error
}

// wsPlayback is the "playback" message: the tape counter's state.
type wsPlayback struct {
	Mode       string        `json:"mode"` // live or playback
	T          time.Time     `json:"t"`
	Speed      float64       `json:"speed"`
	Playing    bool          `json:"playing"`
	GapSkipped *playback.Gap `json:"gapSkipped,omitempty"` // once, in the update after the skip
	TapeStart  *time.Time    `json:"tapeStart,omitempty"`
	TapeEnd    *time.Time    `json:"tapeEnd,omitempty"`
}

type wsError struct {
	Message string `json:"message"`
}

// parseControl decodes one client message; only {type:"playback", data:wsControl} is accepted.
func parseControl(raw []byte) (wsControl, error) {
	var msg struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return wsControl{}, fmt.Errorf("invalid message: %w", err)
	}
	if msg.Type != "playback" {
		return wsControl{}, fmt.Errorf("unknown message type %q", msg.Type)
	}
	var c wsControl
	if err := json.Unmarshal(msg.Data, &c); err != nil {
		return wsControl{}, fmt.Errorf("invalid playback message: %w", err)
	}
	switch c.Action {
	case "play", "pause", "live":
	case "seek":
		if c.T.IsZero() {
			return c, errors.New("seek needs t (RFC 3339)")
		}
	case "speed":
		if !slices.Contains(playback.Speeds, c.Speed) {
			return c, playback.ErrBadSpeed
		}
	case "step":
		if c.Seconds == 0 || math.IsNaN(c.Seconds) || math.Abs(c.Seconds) > 7*24*3600 {
			return c, errors.New("step needs seconds, non-zero and at most a week")
		}
	default:
		return c, fmt.Errorf("unknown playback action %q", c.Action)
	}
	return c, nil
}

// ws serves GET /api/ws: "receiver", "map" and "trails" once, then "trailPoints" (points not sent yet on this
// connection), "aircraft" (sorted by ICAO), "stats" and, with playback enabled, "playback" immediately and every
// StreamInterval; "coverage" immediately and every CoverageInterval, and "coverageGrid" immediately and every
// GridInterval, until the client goes away or the server shuts down (close 1001). A client "playback" message
// switches the connection to a playback session: trails, trailPoints and aircraft then come from the session (a
// full "trails" after every seek), stats and coverage stay live. Invalid control messages get an "error" message.
// A client that stops answering pings or reading is dropped.
func (s *server) ws(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	incoming := make(chan wsIncoming)
	go func() {
		defer cancel()
		conn.SetReadLimit(wsReadLimit)
		_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
		conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(wsPongWait)) })
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			ctl, err := parseControl(raw)
			select {
			case incoming <- wsIncoming{ctl, err}:
			case <-ctx.Done():
				return
			}
		}
	}()

	send := func(typ string, data any) error {
		_ = conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
		return conn.WriteJSON(wsMessage{Type: typ, Data: data})
	}
	var (
		sess    *playback.Session
		changed <-chan struct{}
		seq     uint64 // the session's seek count the trails were sent for
		gapSeq  uint64
		sent    = map[string]int64{}
	)
	defer func() {
		if sess != nil {
			sess.Close()
		}
	}()
	fullTrails := func(tr *tracker.Tracker) error {
		clear(sent)
		return send("trails", wsTrails{Window: tr.TrailWindow().String(), Trails: tr.TrailPointsSince(sent)})
	}
	toLive := func() error {
		sess.Close()
		sess, changed = nil, nil
		return fullTrails(s.Tracker)
	}
	update := func() error {
		tr := s.Tracker
		status := wsPlayback{Mode: "live", T: s.Now(), Speed: 1, Playing: true}
		if sess != nil {
			st := sess.Status()
			if st.Ended {
				if err := toLive(); err != nil {
					return err
				}
			} else {
				status = wsPlayback{Mode: "playback", T: st.T, Speed: st.Speed, Playing: st.Playing}
				if st.GapSeq != gapSeq {
					gapSeq, status.GapSkipped = st.GapSeq, st.Gap
				}
				tr = st.Tracker // nil while the session is freed: the client keeps what it shows
				if tr != nil && st.Seq != seq {
					seq = st.Seq
					if err := fullTrails(tr); err != nil {
						return err
					}
				}
			}
		}
		if tr != nil {
			if err := send("trailPoints", tr.TrailPointsSince(sent)); err != nil {
				return err
			}
			if err := send("aircraft", sortedAircraft(tr)); err != nil {
				return err
			}
		}
		if err := send("stats", s.statsSnapshot()); err != nil {
			return err
		}
		if s.Playback == nil {
			return nil
		}
		oldest, newest := s.Playback.Bounds()
		status.TapeStart, status.TapeEnd = optTime(oldest), optTime(newest)
		status.T = status.T.UTC()
		return send("playback", status)
	}
	control := func(c wsControl) error {
		if c.Action == "live" {
			if sess == nil {
				return nil
			}
			return toLive()
		}
		if s.Playback == nil {
			return errors.New("playback is disabled (tape.enabled, playback.maxSessions)")
		}
		if sess == nil {
			var at time.Time
			switch c.Action {
			case "seek":
				at = c.T
			case "step":
				at = s.Now().Add(time.Duration(c.Seconds * float64(time.Second)))
				if oldest, _ := s.Playback.Bounds(); at.Before(oldest) {
					at = oldest
				}
			default:
				return fmt.Errorf("%s: not in playback, seek first", c.Action)
			}
			ns, err := s.Playback.Start(ctx, at)
			if err != nil {
				return err
			}
			sess, changed, seq, gapSeq = ns, ns.Changed(), 0, 0
			return nil
		}
		switch c.Action {
		case "play":
			return sess.Play()
		case "pause":
			return sess.Pause()
		case "seek":
			return sess.Seek(c.T)
		case "speed":
			return sess.SetSpeed(c.Speed)
		default:
			return sess.Step(time.Duration(c.Seconds * float64(time.Second)))
		}
	}

	if send("receiver", s.Receiver) != nil || send("map", s.Map) != nil || fullTrails(s.Tracker) != nil ||
		send("coverage", s.Coverage.Snapshot()) != nil || send("coverageGrid", s.Coverage.Grid()) != nil || update() != nil {
		return
	}
	ticker := time.NewTicker(s.StreamInterval)
	defer ticker.Stop()
	cov := time.NewTicker(s.CoverageInterval)
	defer cov.Stop()
	grid := time.NewTicker(s.GridInterval)
	defer grid.Stop()
	ping := time.NewTicker(wsPingInterval)
	defer ping.Stop()
	for {
		select {
		case in := <-incoming:
			err := in.err
			if err == nil {
				err = control(in.ctl)
			}
			if err != nil && send("error", wsError{Message: err.Error()}) != nil {
				return
			}
			if update() != nil {
				return
			}
		case <-changed:
			if update() != nil {
				return
			}
		case <-ticker.C:
			if update() != nil {
				return
			}
		case <-cov.C:
			if send("coverage", s.Coverage.Snapshot()) != nil {
				return
			}
		case <-grid.C:
			if send("coverageGrid", s.Coverage.Grid()) != nil {
				return
			}
		case <-ping.C:
			if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteTimeout)) != nil {
				return
			}
		case <-ctx.Done():
			if r.Context().Err() != nil {
				msg := websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down")
				_ = conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(time.Second))
			}
			return
		}
	}
}
