package hosted

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicReportingTransport(t *testing.T) {
	secret := "mlc_" + strings.Repeat("0", 32) + "." + strings.Repeat("a", 43)
	validated := true
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-MLC-Token") != secret || r.Header.Get("X-MLC-Admin-Assertion") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-MLC-Request") != "" {
			t.Fatal("unexpected forwarded credentials")
		}
		w.Header().Set("Content-Type", "application/json")
		if validated {
			w.Header().Set("X-MLC-Reporting-Authorized", "true")
		}
		_, _ = w.Write([]byte(`{"connections":[]}`))
	}))
	defer backend.Close()
	p := proxy{gateway: backend.URL, client: backend.Client(), assets: http.NotFoundHandler()}
	send := func(method, path, token string, want int) {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-MLC-Admin-Assertion", "discard")
		r.Header.Set("Cookie", "a=b")
		r.Header.Set("X-MLC-Token", token)
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("%s %s: %d, want %d", method, path, w.Code, want)
		}
	}
	send("GET", "/api/v1/connections", "", 401)
	send("GET", "/api/v1/connections", secret, 200)
	send("GET", "/api/v1/connections/%40datasets/databases", secret, 200)
	send("GET", "/api/v1/collections?connectionId=%40datasets&database=published", secret, 200)
	send("POST", "/api/v1/query", secret, 200)
	send("DELETE", "/api/v1/schema", secret, 405)
	send("GET", "/api/v1/admin/tokens", secret, 401)
	validated = false
	send("GET", "/api/v1/connections", secret, 502)
	if calls != 5 {
		t.Fatal("unexpected number of forwarded operations", calls)
	}
}
