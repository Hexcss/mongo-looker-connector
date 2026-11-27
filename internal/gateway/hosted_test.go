package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hexcss/mongo-looker-connector/internal/control"
)

type sessionFixture struct{ loginStore }

func (sessionFixture) CheckSession(_ context.Context, raw string) error {
	if raw != strings.Repeat("a", 43) {
		return control.ErrUnauthorized
	}
	return nil
}

func TestHostedSessionsRestrictedToConsole(t *testing.T) {
	s, err := OpenAdmin("", "v", "r", testCatalog{}, func(context.Context, string) bool { return false }, http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	s.EnableHostedLogins(sessionFixture{})
	for _, tc := range []struct {
		path, method string
		header       bool
		want         int
	}{
		{"/api/v1/admin/connections", "GET", false, 200},
		{"/api/v1/query", "POST", true, 401},
		{"/api/v1/admin/preview", "POST", false, 403},
		{"/api/v1/admin/hosted-ticket", "POST", true, 403},
	} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		r.Header.Set("X-MLC-Hosted-Session", strings.Repeat("a", 43))
		if tc.header {
			r.Header.Set("X-MLC-Request", "console")
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%s returned %d", tc.path, w.Code)
		}
	}
}
