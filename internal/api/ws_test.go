package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/coverage"
	"github.com/gorilla/websocket"
)

func wsURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws"
}

type wsIn struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func readWS(t *testing.T, c *websocket.Conn) wsIn {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	var m wsIn
	if err := c.ReadJSON(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestWS_Updates(t *testing.T) {
	srv := httptest.NewServer(newFixture(t).handler)
	defer srv.Close()
	c, _, err := websocket.DefaultDialer.Dial(wsURL(srv), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	m := readWS(t, c)
	if m.Type != "receiver" || string(m.Data) != `{"lat":25.07,"lon":22.87,"maxRangeNm":300}` {
		t.Fatalf("first message = %s %s", m.Type, m.Data)
	}
	m = readWS(t, c)
	if want := `{"tileUrl":"http://tiles.lan/{z}/{x}/{y}.png","attribution":"lan","maxZoom":17,"darkFilter":false}`; m.Type != "map" || string(m.Data) != want {
		t.Fatalf("second message = %s %s", m.Type, m.Data)
	}
	m = readWS(t, c)
	var cov coverage.Snapshot
	if err := json.Unmarshal(m.Data, &cov); m.Type != "coverage" || err != nil || len(cov.Points) < 2 || cov.Receiver.Lat != 25.07 {
		t.Fatalf("third message = %s %s (err %v)", m.Type, m.Data, err)
	}
	m = readWS(t, c)
	var grid coverage.Grid
	if err := json.Unmarshal(m.Data, &grid); m.Type != "coverageGrid" || err != nil || len(grid.Bands) != 4 {
		t.Fatalf("fourth message = %s %s (err %v)", m.Type, m.Data, err)
	}

	// aircraft and stats every 20 ms, coverage every 30 ms, interleaved
	counts := map[string]int{}
	for counts["aircraft"] < 3 || counts["coverage"] < 2 {
		m = readWS(t, c)
		counts[m.Type]++
		switch m.Type {
		case "aircraft":
			var list []map[string]any
			if err := json.Unmarshal(m.Data, &list); err != nil || len(list) != 3 || list[0]["icao"] != "738001" {
				t.Fatalf("aircraft message = %.200s (err %v)", m.Data, err)
			}
			m = readWS(t, c)
			var st statsResponse
			if err := json.Unmarshal(m.Data, &st); m.Type != "stats" || err != nil || st.Aircraft != 3 || st.Source.URL != "tcp://test:30002" {
				t.Fatalf("stats message after aircraft = %s %s (err %v)", m.Type, m.Data, err)
			}
		case "coverage":
		default:
			t.Fatalf("unexpected message %s", m.Type)
		}
	}
}

func TestWS_RejectsBadRequests(t *testing.T) {
	srv := httptest.NewServer(newFixture(t).handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/ws")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("plain GET: code %d, content type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	_, resp, err = websocket.DefaultDialer.Dial(wsURL(srv), http.Header{"Origin": {"http://evil.example"}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin: err %v, resp %v", err, resp)
	}
}

func TestWS_ServerShutdownSendsGoingAway(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	srv := httptest.NewUnstartedServer(newFixture(t).handler)
	srv.Config.BaseContext = func(net.Listener) context.Context { return ctx }
	srv.Start()
	defer srv.Close()

	c, _, err := websocket.DefaultDialer.Dial(wsURL(srv), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	readWS(t, c)
	cancel()

	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, _, err := c.ReadMessage()
		if err == nil {
			continue
		}
		var ce *websocket.CloseError
		if !errors.As(err, &ce) || ce.Code != websocket.CloseGoingAway {
			t.Fatalf("err = %v, want close 1001", err)
		}
		return
	}
}

func TestWS_ClientCloseEndsHandler(t *testing.T) {
	done := make(chan struct{})
	h := newFixture(t).handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
		if r.URL.Path == "/api/ws" {
			close(done)
		}
	}))
	defer srv.Close()

	c, _, err := websocket.DefaultDialer.Dial(wsURL(srv), nil)
	if err != nil {
		t.Fatal(err)
	}
	readWS(t, c)
	_ = c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	c.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler still running after client closed")
	}
}
