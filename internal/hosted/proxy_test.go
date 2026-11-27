package hosted

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBrowserProxy(t *testing.T) {
	ticket, session := strings.Repeat("A", 43), strings.Repeat("B", 43)
	var called int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		if r.Header.Get("X-MLC-Admin-Assertion") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("forwarded incoming credential")
		}
		switch r.URL.Path {
		case "/api/v1/hosted/exchange":
			if r.Header.Get("X-MLC-Login-Ticket") != ticket {
				t.Error("missing ticket")
			}
			json.NewEncoder(w).Encode(map[string]any{"session": session, "expiresAt": time.Now().Add(time.Hour)})
		case "/api/v1/admin/preview", "/api/v1/admin/end-session":
			if r.Header.Get("X-MLC-Hosted-Session") != session || r.Header.Get("X-MLC-Request") != "console" {
				t.Error("missing session")
			}
			w.WriteHeader(204)
		default:
			t.Error("unexpected upstream path")
		}
	}))
	defer up.Close()
	p := proxy{up.URL, up.Client(), http.NotFoundHandler()}
	request := func(path, origin, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://console.example"+path, strings.NewReader(body))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-MLC-Request", "console")
		r.Header.Set("Authorization", "Bearer ignored")
		r.Header.Set("X-MLC-Admin-Assertion", "ignored")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		return w
	}
	origin := "https://console.example"
	w := request("/session/redeem", origin, ticket, nil)
	if w.Code != 204 {
		t.Fatal("ticket exchange", w.Code)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing cookie")
	}
	c := cookies[0]
	if c.Name != cookieName || c.Value != session || c.Path != "/" || c.Domain != "" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode {
		t.Fatal("invalid browser cookie")
	}
	if w := request("/api/v1/admin/preview", origin, "{}", c); w.Code != 204 {
		t.Fatal("preview", w.Code)
	}
	if w := request("/api/v1/admin/preview", "https://other.example", "{}", c); w.Code != 403 {
		t.Fatal("cross-site preview", w.Code)
	}
	if w := request("/api/v1/admin/preview", "", "{}", c); w.Code != 403 {
		t.Fatal("missing origin", w.Code)
	}
	if w := request("/api/v1/admin/preview", origin, "{}", nil); w.Code != 401 {
		t.Fatal("no session", w.Code)
	}
	if w := request("/api/v1/admin/hosted-ticket", origin, "{}", c); w.Code != 404 {
		t.Fatal("public mint endpoint", w.Code)
	}
	if w := request("/session/logout", origin, "", c); w.Code != 303 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout", w.Code)
	}
	if called != 3 {
		t.Fatal("unexpected private gateway call", called)
	}
}
