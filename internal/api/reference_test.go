package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func send(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestReference_AircraftAndRoute(t *testing.T) {
	h := historyHandler(t, true)

	if rec := send(h, http.MethodGet, "/api/reference/aircraft/4840d6", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET before PUT: %d", rec.Code)
	}
	rec := send(h, http.MethodPut, "/api/reference/aircraft/4840d6",
		`{"registration":" ph-bxa ","typeCode":"b738","typeName":"Boeing 737-800","operator":"KLM"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body)
	}
	var a struct{ ICAO, Registration, TypeCode, TypeName, Operator, Updated string }
	if code := getJSON(t, h, "/api/reference/aircraft/4840D6", &a); code != http.StatusOK {
		t.Fatalf("GET: %d", code)
	}
	if a.ICAO != "4840D6" || a.Registration != "PH-BXA" || a.TypeCode != "B738" || a.TypeName != "Boeing 737-800" || a.Updated == "" {
		t.Errorf("GET = %+v", a)
	}
	if rec := send(h, http.MethodPut, "/api/reference/aircraft/4840D6", `{}`); rec.Code != http.StatusNoContent {
		t.Errorf("empty PUT: %d, want 204", rec.Code)
	}
	if rec := send(h, http.MethodGet, "/api/reference/aircraft/4840D6", ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET after empty PUT: %d", rec.Code)
	}

	if rec := send(h, http.MethodPut, "/api/reference/routes/klm1023", `{"origin":"EHAM Amsterdam","destination":"LEMD"}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT route: %d %s", rec.Code, rec.Body)
	}
	var rt struct{ Callsign, Origin, Destination string }
	if code := getJSON(t, h, "/api/reference/routes/KLM1023", &rt); code != http.StatusOK || rt.Origin != "EHAM Amsterdam" || rt.Destination != "LEMD" {
		t.Errorf("GET route = %d %+v", code, rt)
	}
	if rec := send(h, http.MethodDelete, "/api/reference/routes/KLM1023", ""); rec.Code != http.StatusNoContent {
		t.Errorf("DELETE route: %d", rec.Code)
	}
	if rec := send(h, http.MethodGet, "/api/reference/routes/KLM1023", ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET deleted route: %d", rec.Code)
	}
}

func TestReference_Invalid(t *testing.T) {
	h := historyHandler(t, true)
	for _, c := range []struct {
		method, path, body string
		ct                 bool
		want               int
	}{
		{http.MethodGet, "/api/reference/aircraft/XYZ", "", false, http.StatusBadRequest},
		{http.MethodGet, "/api/reference/routes/KLM-1", "", false, http.StatusBadRequest},
		{http.MethodPut, "/api/reference/routes/KLM1", `{"origin":"A"}`, false, http.StatusUnsupportedMediaType},
		{http.MethodPut, "/api/reference/routes/KLM1", `{"via":"A"}`, true, http.StatusBadRequest},
		{http.MethodPut, "/api/reference/routes/KLM1", `{"origin":"` + strings.Repeat("x", 65) + `"}`, true, http.StatusBadRequest},
	} {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		if c.ct {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s %s %s: %d, want %d", c.method, c.path, c.body, rec.Code, c.want)
		}
	}
	if rec := send(historyHandler(t, false), http.MethodGet, "/api/reference/aircraft/4840D6", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("without history: %d, want 503", rec.Code)
	}
}
