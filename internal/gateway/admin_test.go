package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hexcss/mongo-looker-connector/internal/control"
)

type testCatalog struct {
	catalog
	token control.Token
}

func (s testCatalog) Authenticate(_ context.Context, value string) (control.Token, error) {
	if value != "mlc_valid" {
		return control.Token{}, control.ErrUnauthorized
	}
	return s.token, nil
}
func (testCatalog) Connections(context.Context) ([]control.Connection, error) { return nil, nil }
func (testCatalog) GetConnection(context.Context, string) (control.Connection, error) {
	return control.Connection{}, control.ErrNotFound
}

func TestAdminAccessAndScopedDiscovery(t *testing.T) {
	t.Setenv("GATEWAY_TEST_URI", "mongodb://127.0.0.1:1/?serverSelectionTimeoutMS=20")
	s, err := Open(config, "v", "r")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	store := testCatalog{token: control.Token{Grants: []control.Grant{{ConnectionID: "demo", Database: "analytics", Collection: "orders", Generation: s.connections["demo"].generation}}}}
	s.EnableAdmin(store, func(_ context.Context, v string) bool { return v == "signed-proof" }, http.NotFoundHandler())
	for _, tc := range []struct {
		method, path, credential, body string
		want                           int
	}{
		{"GET", "/api/v1/connections", "", "", 401},
		{"GET", "/api/v1/connections", "invalid", "", 401},
		{"GET", "/api/v1/connections", "mlc_valid", "", 200},
		{"GET", "/api/v1/admin/connections", "mlc_valid", "", 403},
		{"GET", "/api/v1/admin/connections", "admin", "", 200},
		{"POST", "/api/v1/admin/tokens", "admin", "{}", 403},
		{"POST", "/api/v1/query", "mlc_valid", `{"connectionId":"demo","database":"analytics","collection":"other","fields":["amount"]}`, 404},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		if tc.credential == "admin" {
			r.Header.Set("X-MLC-Admin-Assertion", "signed-proof")
		} else if tc.credential != "" {
			r.Header.Set("Authorization", "Bearer "+tc.credential)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s %s (%s): %d %s", tc.method, tc.path, tc.credential, w.Code, w.Body.String())
		}
	}
}

func TestGrantsIntersectCurrentAllowlistAndGeneration(t *testing.T) {
	c := control.Connection{ID: "demo", Generation: "new", Databases: map[string][]string{"analytics": {"orders", "items"}}}
	token := control.Token{Grants: []control.Grant{
		{ConnectionID: "demo", Generation: "old", Database: "analytics", Collection: "items"},
		{ConnectionID: "demo", Generation: "new", Database: "analytics", Collection: "orders"},
		{ConnectionID: "demo", Generation: "new", Database: "analytics", Collection: "removed"},
	}}
	dbs := allowedDatabases(c, &token)
	if len(dbs) != 1 || len(dbs["analytics"]) != 1 || dbs["analytics"][0] != "orders" {
		t.Fatalf("unexpected grants %#v", dbs)
	}
}

func TestManagedURI(t *testing.T) {
	for _, u := range []string{
		"file:///etc/private", "mongodb://db.example/?tls=false", "mongodb://db.example/?tls=true&tlsInsecure=true",
		"mongodb://db.example/?tls=true&tlsCAFile=/some/file",
		"mongodb://db.example/?tls=true&authMechanism=MONGODB-OIDC",
	} {
		if validManagedURI(u) {
			t.Errorf("accepted %s", u)
		}
	}
	for _, u := range []string{
		"mongodb+srv://reader:password@report.example/analytics",
		"mongodb://reader:password@db.example:27017/analytics?tls=true",
		"mongodb://fixture.europe-west1.firestore.goog:443/mlc-sandbox?loadBalanced=true&tls=true&retryWrites=false&authMechanism=MONGODB-OIDC&authMechanismProperties=ENVIRONMENT:gcp,TOKEN_RESOURCE:FIRESTORE",
	} {
		if !validManagedURI(u) {
			t.Errorf("rejected supported fixture form")
		}
	}
}
