package console

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConsoleServesEmbeddedAssets(t *testing.T) {
	handler := Handler()
	for _, path := range []string{"/", "/console.css", "/console.js", "/icons.svg"} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusOK || res.Body.Len() == 0 {
			t.Errorf("%s: empty or unavailable asset (%d)", path, res.Code)
		}
		if path == "/" && (!strings.Contains(res.Header().Get("Content-Security-Policy"), "script-src 'self'") || res.Header().Get("Cache-Control") != "no-store") {
			t.Error("console document missing browser protections")
		}
	}
}
