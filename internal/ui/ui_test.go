package ui

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fourcourtjester/single-studio-gamestate/internal/adapter"
	"github.com/fourcourtjester/single-studio-gamestate/internal/control"
	"github.com/fourcourtjester/single-studio-gamestate/internal/relay"
)

func newPanel(t *testing.T) (http.Handler, *control.ErrorLog) {
	t.Helper()
	errs := &control.ErrorLog{}
	log := slog.New(errs.Handler(slog.NewTextHandler(io.Discard, nil)))
	hub := relay.NewHub(nil, log)
	ctrl := control.New(adapter.Options{Bind: "127.0.0.1"}, hub, log)
	t.Cleanup(ctrl.Stop)
	mux := http.NewServeMux()
	(&Server{Ctrl: ctrl, Errors: errs, Hub: hub, Bind: "127.0.0.1"}).Register(mux)
	return mux, errs
}

func do(h http.Handler, method, target, body string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = "127.0.0.1:47600"
	for k, v := range header {
		if k == "Host" {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPanelPage(t *testing.T) {
	h, _ := newPanel(t)
	rec := do(h, "GET", "/", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Single Studio Companion") {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestSelectAndState(t *testing.T) {
	h, errs := newPanel(t)
	if rec := do(h, "POST", "/api/game", `{"game":"war3"}`, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("select: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", "/api/start", "", nil); rec.Code != http.StatusConflict {
		t.Fatalf("start war3: %d", rec.Code)
	}

	rec := do(h, "GET", "/api/state", "", nil)
	var st struct {
		Game    string          `json:"game"`
		Running bool            `json:"running"`
		Relay   string          `json:"relay"`
		Games   []adapter.Title `json:"games"`
		Errors  []control.Entry `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Game != "war3" || st.Running || st.Relay != "ws://127.0.0.1:47600/ws" || len(st.Games) != len(adapter.Titles) {
		t.Fatalf("state: %+v", st)
	}
	if len(st.Errors) != 1 || !strings.Contains(st.Errors[0].Message, "Warcraft III") {
		t.Fatalf("errors: %+v", st.Errors)
	}

	do(h, "POST", "/api/errors/clear", "", nil)
	if n := len(errs.Entries()); n != 0 {
		t.Fatalf("%d errors after clear", n)
	}
	if rec := do(h, "POST", "/api/game", `{"game":"valorant"}`, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown game: %d", rec.Code)
	}
}

func TestGuard(t *testing.T) {
	h, _ := newPanel(t)
	cases := []struct {
		name   string
		header map[string]string
		want   int
	}{
		{"same origin", map[string]string{"Origin": "http://127.0.0.1:47600"}, http.StatusNoContent},
		{"localhost", map[string]string{"Host": "localhost:47600"}, http.StatusNoContent},
		{"other site", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"dns rebinding", map[string]string{"Host": "evil.example:47600"}, http.StatusForbidden},
	}
	for _, c := range cases {
		if rec := do(h, "POST", "/api/stop", "", c.header); rec.Code != c.want {
			t.Errorf("%s: got %d, want %d", c.name, rec.Code, c.want)
		}
	}
}
