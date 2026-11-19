package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	wsWriteTimeout = 10 * time.Second
	wsPingInterval = 30 * time.Second
	wsPongWait     = 2 * wsPingInterval
	wsReadLimit    = 4096
)

// wsMessage is one WebSocket text message: Type is "receiver", "map", "aircraft" or "stats".
type wsMessage struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Default CheckOrigin: only same-origin browsers may connect (the Vite dev proxy rewrites Origin).
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 16 * 1024,
	Error: func(w http.ResponseWriter, _ *http.Request, status int, reason error) {
		writeError(w, status, reason.Error())
	},
}

// ws serves GET /api/ws: "receiver" and "map" once, then "aircraft" (sorted by ICAO) and "stats" immediately and every
// StreamInterval, until the client goes away or the server shuts down (close 1001). Client messages are
// ignored; a client that stops answering pings or reading is dropped.
func (s *server) ws(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		defer cancel()
		conn.SetReadLimit(wsReadLimit)
		_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
		conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(wsPongWait)) })
		for {
			if _, _, err := conn.NextReader(); err != nil {
				return
			}
		}
	}()

	send := func(typ string, data any) error {
		_ = conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
		return conn.WriteJSON(wsMessage{Type: typ, Data: data})
	}
	update := func() error {
		if err := send("aircraft", s.aircraftByICAO()); err != nil {
			return err
		}
		return send("stats", s.statsSnapshot())
	}

	if send("receiver", s.Receiver) != nil || send("map", s.Map) != nil || update() != nil {
		return
	}
	ticker := time.NewTicker(s.StreamInterval)
	defer ticker.Stop()
	ping := time.NewTicker(wsPingInterval)
	defer ping.Stop()
	for {
		select {
		case <-ticker.C:
			if update() != nil {
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
