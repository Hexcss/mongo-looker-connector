package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hexcss/mongo-looker-connector/internal/control"
)

type teamFixture struct {
	loginStore
	accounts map[string]control.Account
	sessions map[string]control.Account
}

func (f *teamFixture) Account(_ context.Context, id string) (control.Account, error) {
	a, ok := f.accounts[id]
	if !ok {
		return a, control.ErrNotFound
	}
	return a, nil
}
func (f *teamFixture) Accounts(context.Context) ([]control.Account, error) {
	var accounts []control.Account
	for _, a := range f.accounts {
		accounts = append(accounts, a)
	}
	return accounts, nil
}
func (f *teamFixture) CreateAccount(_ context.Context, a control.Account) error {
	if _, ok := f.accounts[a.ID]; ok {
		return control.ErrConflict
	}
	f.accounts[a.ID] = a
	return nil
}
func (f *teamFixture) BootstrapAccount(ctx context.Context, a control.Account) error {
	for _, existing := range f.accounts {
		if existing.Primary {
			return control.ErrConflict
		}
	}
	return f.CreateAccount(ctx, a)
}
func (f *teamFixture) UpdateAccount(ctx context.Context, a control.Account, old string) error {
	current, err := f.Account(ctx, a.ID)
	if err != nil {
		return err
	}
	if current.Revision != old {
		return control.ErrConflict
	}
	f.accounts[a.ID] = a
	return nil
}
func (f *teamFixture) MintAccountSession(_ context.Context, a control.Account) (string, time.Time, error) {
	key := strings.Repeat(string(rune('a'+len(f.sessions))), 43)
	f.sessions[key] = a
	return key, time.Now().Add(time.Hour), nil
}
func (f *teamFixture) SessionAccount(ctx context.Context, key string) (control.Account, error) {
	a, ok := f.sessions[key]
	if !ok {
		return a, control.ErrUnauthorized
	}
	current, err := f.Account(ctx, a.ID)
	if err != nil || current.Revision != a.Revision || current.Disabled {
		return control.Account{}, control.ErrUnauthorized
	}
	return current, nil
}

func TestTeamAccountsAndRevocation(t *testing.T) {
	catalog := &datasetTestCatalog{dataset: control.Dataset{ID: "sales", ConnectionID: "sensitive-source", Database: "internal", Collection: "orders", Pipeline: []byte(`[{"$limit":10}]`)}}
	s, err := OpenAdmin("", "v", "r", catalog, func(_ context.Context, proof string) bool { return proof == "signed" }, http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	team := &teamFixture{accounts: map[string]control.Account{}, sessions: map[string]control.Account{}}
	s.EnableHostedLogins(team)
	request := func(method, path, identity string, body any, status int) []byte {
		t.Helper()
		var buf bytes.Buffer
		if body != nil {
			json.NewEncoder(&buf).Encode(body)
		}
		r := httptest.NewRequest(method, "/api/v1/"+path, &buf)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-MLC-Request", "console")
		if identity == "operator" {
			r.Header.Set("X-MLC-Admin-Assertion", "signed")
		} else if identity != "" {
			r.Header.Set("X-MLC-Hosted-Session", identity)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: HTTP %d, want %d", method, path, w.Code, status)
		}
		return w.Body.Bytes()
	}
	fields := func(raw []byte) map[string]any {
		t.Helper()
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	post := func(path, identity string, body any, status int) []byte {
		return request("POST", path, identity, body, status)
	}
	login := func(email, password string, status int) string {
		reply := fields(post("hosted/login", "", map[string]string{"email": email, "password": password}, status))
		if status != 200 {
			return ""
		}
		return reply["session"].(string)
	}
	const email = "member@example.invalid"
	request("GET", "admin/users", "", nil, 401)
	post("admin/bootstrap-owner", "", map[string]string{"email": email}, 401)
	primary := fields(post("admin/bootstrap-owner", "operator", map[string]string{"email": "owner@example.invalid"}, 201))
	post("admin/bootstrap-owner", "operator", map[string]string{"email": "another@example.invalid"}, 409)
	temp := fields(post("admin/users", "operator", map[string]string{"email": email, "role": "viewer"}, 201))["temporaryPassword"].(string)
	session := login(email, temp, 200)
	if me := fields(request("GET", "admin/me", session, nil, 200)); me["mustChangePassword"] != true || me["role"] != "viewer" {
		t.Fatal("wrong initial account")
	}
	request("GET", "admin/datasets", session, nil, 403)
	newPassword := "a changed demo password"
	post("admin/password", session, map[string]string{"currentPassword": "incorrect", "newPassword": newPassword}, 403)
	post("admin/password", session, map[string]string{"currentPassword": temp, "newPassword": newPassword}, 204)
	request("GET", "admin/me", session, nil, 401)
	login(email, temp, 401)
	session = login(email, newPassword, 200)
	if got := string(request("GET", "admin/datasets", session, nil, 200)); got != `{"datasets":[{"id":"sales"}]}` {
		t.Fatalf("viewer inventory contained private metadata: %s", got)
	}
	for _, route := range []string{"admin/connections", "admin/tokens", "admin/users"} {
		request("GET", route, session, nil, 403)
	}
	post("admin/workspace/preview", session, map[string]string{}, 403)
	post("admin/workspace/explain", session, map[string]string{}, 403)
	id, _ := control.AccountID(email)
	request("PUT", "admin/users/"+id, "operator", map[string]any{"role": "editor", "disabled": false}, 200)
	request("GET", "admin/datasets", session, nil, 401)
	session = login(email, newPassword, 200)
	request("GET", "admin/connections", session, nil, 200)
	post("admin/workspace/preview", session, map[string]string{}, 400)
	post("admin/workspace/explain", session, map[string]string{}, 400)
	post("admin/workspace/open", session, map[string]string{}, 400)
	request("GET", "admin/users", session, nil, 403)
	request("DELETE", "admin/datasets/sales", session, nil, 403)
	post("admin/tokens", session, map[string]string{}, 403)
	reset := fields(post("admin/users/"+id+"/reset-password", "operator", map[string]string{}, 200))["temporaryPassword"].(string)
	request("GET", "admin/datasets", session, nil, 401)
	session = login(email, reset, 200)
	request("PUT", "admin/users/"+id, "operator", map[string]any{"role": "editor", "disabled": true}, 200)
	request("GET", "admin/me", session, nil, 401)
	login(email, reset, 401)
	ownerID := primary["user"].(map[string]any)["id"].(string)
	request("PUT", "admin/users/"+ownerID, "operator", map[string]any{"role": "viewer", "disabled": false}, 403)
}

func TestLoginThrottle(t *testing.T) {
	s, err := OpenAdmin("", "v", "r", testCatalog{}, nil, http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	s.EnableHostedLogins(&teamFixture{})
	for i := range 9 {
		r := httptest.NewRequest("POST", "/api/v1/hosted/login", strings.NewReader(`{"email":"absent@example.invalid","password":"`+strings.Repeat("x", 73)+`"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if (i < 8 && w.Code != 401) || (i == 8 && (w.Code != 429 || w.Header().Get("Retry-After") == "")) {
			t.Fatal("unexpected login throttle", i, w.Code)
		}
	}
}
