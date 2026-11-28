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
	p := proxy{gateway: up.URL, client: up.Client(), assets: http.NotFoundHandler()}
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

func TestPasswordLoginUsesProtectedCookie(t *testing.T) {
	session := strings.Repeat("Q", 43)
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-MLC-Admin-Assertion") != "" {
			t.Error("forwarded ambient credentials")
		}
		if r.URL.Path == "/api/v1/hosted/login" {
			var body map[string]string
			if r.Header.Get("X-MLC-Hosted-Session") != "" || json.NewDecoder(r.Body).Decode(&body) != nil || body["email"] != "employee@example.invalid" {
				t.Error("invalid login body or header")
			}
			json.NewEncoder(w).Encode(map[string]any{"session": session, "expiresAt": time.Now().Add(time.Hour)})
		} else if r.URL.Path == "/api/v1/admin/password" && r.Header.Get("X-MLC-Hosted-Session") == session {
			w.WriteHeader(204)
		} else {
			t.Error("unexpected upstream route")
		}
	}))
	defer up.Close()
	p, err := OpenStandalone(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	send := func(path, origin, site, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://localhost:8080"+path, strings.NewReader(body))
		r.Header.Set("Origin", origin)
		r.Header.Set("Sec-Fetch-Site", site)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-MLC-Request", "console")
		r.Header.Set("X-MLC-Admin-Assertion", "ignored")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		return w
	}
	for _, u := range []string{"http://other.example", "null", ""} {
		if w := send("/session/login", u, "", "{}", nil); w.Code != 403 {
			t.Fatal("accepted foreign sign-in origin")
		}
	}
	if w := send("/session/login", "http://localhost:8080", "cross-site", "{}", nil); w.Code != 403 {
		t.Fatal("accepted cross-site sign-in")
	}
	origin := "http://localhost:8080"
	w := send("/session/login", origin, "same-origin", `{"email":"employee@example.invalid","password":"a sample password"}`, nil)
	if w.Code != 204 || w.Body.Len() != 0 {
		t.Fatal("password login did not set a cookie")
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("insecure password session")
	}
	w = send("/api/v1/admin/password", origin, "same-origin", "{}", cookies[0])
	if w.Code != 204 || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("password change retained session")
	}
	if calls != 2 {
		t.Fatal("unexpected upstream calls", calls)
	}
}
