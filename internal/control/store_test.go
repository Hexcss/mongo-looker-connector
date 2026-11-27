package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/option"
	sm "google.golang.org/api/secretmanager/v1"
)

func TestBearer(t *testing.T) {
	a, raw := newBearer()
	b, other := newBearer()
	if raw == other || a.ID == b.ID || a.ID == "" || len(raw) < 60 || !strings.HasPrefix(raw, "mlc_") {
		t.Fatal("bearers should have unique IDs and independent secrets")
	}
	if bearerID(raw) != a.ID || !a.matches(raw) || a.matches(other) {
		t.Fatal("bearer verification accepted the wrong credential")
	}
	a.Revoked = true
	if a.matches(raw) {
		t.Fatal("accepted a revoked credential")
	}
	for _, bad := range []string{"", "mlc_", "mlc_/" + raw, raw + " ", raw + ".", strings.ToUpper(raw)} {
		if bearerID(bad) != "" {
			t.Errorf("parsed a malformed bearer")
		}
	}
}

func TestPinnedVersions(t *testing.T) {
	const parent = "projects/test-project/secrets/control"
	s := &Store{secret: parent}
	for _, resource := range []string{parent + "/versions/1", "projects/12345/secrets/control/versions/1"} {
		version, err := s.pinned(resource)
		if err != nil || version != parent+"/versions/1" {
			t.Fatalf("canonical pin: %v", err)
		}
	}
	for _, resource := range []string{parent + "/versions/latest", parent + "/versions/0", parent + "/versions/+1", parent + "/versions/02", "projects/test-project/secrets/other/versions/1", "junk/versions/1"} {
		if _, err := s.pinned(resource); err == nil {
			t.Fatalf("accepted invalid pin %s", resource)
		}
	}
}

func TestInvalidControlInputFailsBeforeStorage(t *testing.T) {
	s := &Store{}
	ctx := context.Background()
	if _, err := s.GetConnection(ctx, "a/b"); !errors.Is(err, ErrInvalid) {
		t.Errorf("invalid document ID: %v", err)
	}
	if _, err := s.SaveConnection(ctx, "new", map[string][]string{"demo": {"orders"}}, "", ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("missing initial credential: %v", err)
	}
	if _, err := s.SaveConnection(ctx, "new", map[string][]string{"demo": {"orders", "orders"}}, "mongodb://host", ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("duplicate allowlist entry: %v", err)
	}
	if err := s.DeleteConnection(ctx, "new", ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("unconditional delete: %v", err)
	}
	for _, grants := range [][]Grant{nil, {{ConnectionID: "demo", Database: "a", Collection: "b"}}} {
		if _, _, err := s.Issue(ctx, "reports", grants); !errors.Is(err, ErrInvalid) {
			t.Errorf("missing concrete grant or generation: %v", err)
		}
	}
	if _, err := s.Authenticate(ctx, "mlc_invalid"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("bad bearer: %v", err)
	}
}

// Run against a disposable local Firestore with FIRESTORE_EMULATOR_HOST set.
// Secret Manager's HTTP boundary is a fixture; no credentials are required.
func TestStoreEmulator(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("set FIRESTORE_EMULATOR_HOST to run the persistence test")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	db, err := firestore.NewClientWithDatabase(ctx, "mlc-test", fmt.Sprintf("control-%x", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const secret = "projects/mlc-test/secrets/control"
	var mu sync.Mutex
	payloads := map[string]*sm.SecretPayload{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/v1/")
		switch {
		case path == secret+":addVersion" && r.Method == "POST":
			var body sm.AddSecretVersionRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			id := secret + "/versions/" + strconv.Itoa(1+len(payloads))
			payloads[id] = body.Payload
			json.NewEncoder(w).Encode(sm.SecretVersion{Name: id})
		case strings.HasSuffix(path, ":access"):
			id := strings.TrimSuffix(path, ":access")
			if payloads[id] == nil {
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(sm.AccessSecretVersionResponse{Name: id, Payload: payloads[id]})
		case strings.HasSuffix(path, ":destroy"):
			id := strings.TrimSuffix(path, ":destroy")
			payloads[id] = nil
			json.NewEncoder(w).Encode(sm.SecretVersion{Name: id, State: "DESTROYED"})
		case path == secret+"/versions":
			var versions []*sm.SecretVersion
			for id, data := range payloads {
				if data != nil {
					versions = append(versions, &sm.SecretVersion{Name: id, State: "ENABLED", CreateTime: "2000-01-01T00:00:00Z"})
				}
			}
			json.NewEncoder(w).Encode(sm.ListSecretVersionsResponse{Versions: versions})
		default:
			t.Errorf("unexpected Secret Manager request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	service, err := sm.NewService(ctx, option.WithEndpoint(server.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{db: db, secrets: service, secret: secret, owner: "mlc-test/control"}
	ns := map[string][]string{"analytics": {"orders"}, "__source__": {"orders"}}
	first, err := s.SaveConnection(ctx, "demo", ns, "mongodb://reader:fixture@first", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveConnection(ctx, "demo", ns, "mongodb://host", ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate create: %v", err)
	}
	read, err := s.GetConnection(ctx, "demo")
	if err != nil || !reflect.DeepEqual(read, first) {
		t.Fatalf("get after create: %v", err)
	}
	list, err := s.Connections(ctx)
	if err != nil || len(list) != 1 || list[0].ID != "demo" {
		t.Fatalf("list after create: %v", err)
	}
	uri, err := s.URI(ctx, read)
	if err != nil || uri != "mongodb://reader:fixture@first" {
		t.Fatalf("access pinned version: %v", err)
	}
	meta, err := s.connection("demo").Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprint(meta.Data()), "fixture") {
		t.Fatal("credential leaked into Firestore")
	}
	public, _ := json.Marshal(first)
	if strings.Contains(string(public), "fixture") || strings.Contains(string(public), "versions/") {
		t.Fatal("credential storage exposed in connection JSON")
	}

	grant := Grant{ConnectionID: "demo", Database: "analytics", Collection: "orders", Generation: first.Generation}
	token, raw, err := s.Issue(ctx, "reports", []Grant{grant})
	if err != nil {
		t.Fatal(err)
	}
	tokenDoc, err := db.Collection("tokens").Doc(token.ID).Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprint(tokenDoc.Data()), raw) {
		t.Fatal("raw bearer persisted")
	}
	auth, err := s.Authenticate(ctx, raw)
	if err != nil || len(auth.Grants) != 1 || auth.Grants[0] != grant {
		t.Fatalf("authentication: %v", err)
	}
	_, other := newBearer()
	if _, err := s.Authenticate(ctx, strings.Split(raw, ".")[0]+"."+strings.Split(other, ".")[1]); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong secret: %v", err)
	}
	if err := s.Revoke(ctx, token.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, raw); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revocation: %v", err)
	}
	tokens, err := s.Tokens(ctx)
	if err != nil || len(tokens) != 1 || !tokens[0].Revoked {
		t.Fatalf("token list: %v", err)
	}

	refresh, err := s.SaveConnection(ctx, "demo", ns, "", first.Revision)
	if err != nil || refresh.Generation != first.Generation || refresh.Revision == first.Revision {
		t.Fatalf("metadata update changed incarnation or reused revision: %v", err)
	}
	if _, err := s.SaveConnection(ctx, "demo", ns, "", first.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update: %v", err)
	}
	if err := s.DeleteConnection(ctx, "demo", first.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale delete: %v", err)
	}
	rotated, err := s.SaveConnection(ctx, "demo", ns, "mongodb://reader:fixture@second", refresh.Revision)
	if err != nil || rotated.Generation == refresh.Generation {
		t.Fatalf("rotation must invalidate old grants: %v", err)
	}
	if got, err := s.URI(ctx, first); err != nil || got != "mongodb://reader:fixture@first" {
		t.Fatalf("in-flight reader lost its pinned credential: %v", err)
	}
	if err := s.cleanup(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.URI(ctx, first); err == nil {
		t.Fatal("superseded credential still accessible after grace period")
	}
	if got, err := s.URI(ctx, rotated); err != nil || got != "mongodb://reader:fixture@second" {
		t.Fatalf("current credential removed: %v", err)
	}
	if err := s.DeleteConnection(ctx, "demo", rotated.Revision); err != nil {
		t.Fatal(err)
	}
	recreated, err := s.SaveConnection(ctx, "demo", ns, "mongodb://reader:fixture@third", "")
	if err != nil || recreated.Generation == first.Generation || recreated.Generation == rotated.Generation {
		t.Fatalf("recreated ID revived an old grant: %v", err)
	}
	// An interrupted write can leave a version with no Firestore claim.
	orphan, err := s.addURI(ctx, "mongodb://reader:fixture@orphan")
	if err != nil {
		t.Fatal(err)
	}
	foreign := &Store{secrets: service, secret: secret, owner: "different/deployment"}
	unrelated, err := foreign.addURI(ctx, "mongodb://reader:fixture@unrelated")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.cleanup(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.readURI(ctx, orphan); err == nil {
		t.Fatal("abandoned credential still exists")
	}
	if _, err := s.claim(orphan).Create(ctx, map[string]any{"retired": false}); err == nil {
		t.Fatal("a delayed writer could publish the reclaimed version")
	}
	if _, err := foreign.readURI(ctx, unrelated); err != nil {
		t.Fatal("cleanup destroyed another deployment's credential")
	}

	firstWriter := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-firstWriter
			_, err := s.SaveConnection(ctx, "demo", ns, "mongodb://reader:fixture@new", recreated.Revision)
			results <- err
		}()
	}
	close(firstWriter)
	one, two := <-results, <-results
	if (one == nil) == (two == nil) || (one != nil && !errors.Is(one, ErrConflict)) || (two != nil && !errors.Is(two, ErrConflict)) {
		t.Fatalf("two writers with one revision: %v / %v", one, two)
	}
	if _, err := s.SaveConnection(ctx, "__archive__", ns, "mongodb://reader:fixture@fourth", ""); err != nil {
		t.Fatalf("valid connection ID: %v", err)
	}

}
