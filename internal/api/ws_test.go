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
	if m.Type != "receiver" || string(m.Data) != `{"lat":32.09,"lon":34.96,"maxRangeNm":300}` {
		t.Fatalf("first message = %s %s", m.Type, m.Data)
	}
	for tick := range 2 {
		m = readWS(t, c)
		var list []map[string]any
		if err := json.Unmarshal(m.Data, &list); m.Type != "aircraft" || err != nil || len(list) != 3 || list[0]["icao"] != "738001" {
			t.Fatalf("tick %d: aircraft message = %s %.200s (err %v)", tick, m.Type, m.Data, err)
		}
		m = readWS(t, c)
		var st statsResponse
		if err := json.Unmarshal(m.Data, &st); m.Type != "stats" || err != nil || st.Aircraft != 3 || st.Source.URL != "tcp://test:30002" {
			t.Fatalf("tick %d: stats message = %s %s (err %v)", tick, m.Type, m.Data, err)
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
