package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Hexcss/mongo-looker-connector/internal/control"
	"github.com/Hexcss/mongo-looker-connector/internal/engine"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/drivertest"
)

type datasetTestCatalog struct {
	testCatalog
	dataset control.Dataset
	tokens  map[string]control.Token
}

func (c *datasetTestCatalog) Datasets(context.Context) ([]control.Dataset, error) {
	return []control.Dataset{c.dataset}, nil
}
func (c *datasetTestCatalog) GetDataset(_ context.Context, id string) (control.Dataset, error) {
	if id != c.dataset.ID {
		return control.Dataset{}, control.ErrNotFound
	}
	return c.dataset, nil
}
func (c *datasetTestCatalog) Authenticate(_ context.Context, raw string) (control.Token, error) {
	token, ok := c.tokens[raw]
	if !ok {
		return control.Token{}, control.ErrUnauthorized
	}
	return token, nil
}

func TestDatasetWarmCacheFollowsLivePermissions(t *testing.T) {
	reply := bson.D{{Key: "ok", Value: 1}, {Key: "cursor", Value: bson.M{
		"id": int64(0), "ns": "analytics.orders", "firstBatch": bson.A{bson.M{"c0": "one"}},
	}}}
	opts := options.Client()
	opts.Deployment = drivertest.NewMockDeployment(reply, reply, reply)
	client, err := mongo.Connect(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	srv := Server{mux: http.NewServeMux(), connections: map[string]connection{
		"demo": {client: client, generation: "source-generation", databases: map[string][]string{"analytics": {"orders", "zones"}}},
	}}
	srv.routes("dev", "unknown")
	d := control.Dataset{ID: "revenue", ConnectionID: "demo", Database: "analytics", Collection: "orders",
		Pipeline: json.RawMessage(`[{"$lookup":{"from":"zones","localField":"_id","foreignField":"_id","as":"zone"}},{"$match":{"sales":42}}]`), SourceGeneration: "source-generation", Revision: "v1",
		Fields: []engine.Field{{Name: "_id", Type: "TEXT"}, {Name: "sales", Type: "NUMBER"}}}
	grant := func(fields ...string) []control.Grant {
		return []control.Grant{{ConnectionID: control.DatasetConnection, Database: control.DatasetDatabase, Collection: d.ID, Generation: d.Revision, Fields: fields}}
	}
	store := &datasetTestCatalog{dataset: d, tokens: map[string]control.Token{
		"mlc_wide": {ID: "one", Grants: grant("_id", "sales")}, "mlc_narrow": {ID: "two", Grants: grant("_id")},
		"mlc_raw": {ID: "three", Grants: []control.Grant{{ConnectionID: "demo", Database: "analytics", Collection: "orders", Generation: "source-generation"}}},
	}}
	srv.EnableAdmin(store, func(context.Context, string) bool { return false }, http.NotFoundHandler())
	request := func(token, path, body string, code int, cache string) []byte {
		t.Helper()
		method := "GET"
		if body != "" {
			method = "POST"
		}
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		if w.Code != code || w.Header().Get("X-MLC-Cache") != cache {
			t.Fatalf("%s %s: status %d (%s), cache %q, want %d %q", token, path, w.Code, w.Body.String(), w.Header().Get("X-MLC-Cache"), code, cache)
		}
		return w.Body.Bytes()
	}
	const query = "/api/v1/query"
	const prefix = `{"connectionId":"@datasets","database":"published","collection":"revenue"`
	const ids = prefix + `,"fields":["_id"]}`
	if got := string(request("mlc_narrow", "/api/v1/connections", "", 200, "")); got != `{"connections":["@datasets"]}` {
		t.Fatal(got)
	}
	request("mlc_narrow", "/api/v1/connections/%40datasets/databases", "", 200, "")
	request("mlc_narrow", "/api/v1/collections?connectionId=%40datasets&database=published", "", 200, "")
	visible := string(request("mlc_narrow", "/api/v1/schema", prefix+"}", 200, ""))
	if visible != `{"fields":[{"name":"_id","type":"TEXT"}]}` {
		t.Fatalf("private fields in schema %s", visible)
	}
	request("mlc_narrow", query, `{"connectionId":"demo","database":"analytics","collection":"orders","fields":["sales"]}`, 404, "")
	request("mlc_raw", query, ids, 404, "")
	data := request("mlc_wide", query, ids, 200, "MISS")
	if string(data) != `{"fields":["_id"],"rows":[["one"]]}` {
		t.Fatalf("unexpected managed result: %s", data)
	}
	if !bytes.Equal(data, request("mlc_wide", query, ids, 200, "HIT")) {
		t.Fatal("changed cached result")
	}
	request("mlc_narrow", query, ids, 200, "MISS")
	request("mlc_narrow", query, ids, 200, "HIT")
	request("mlc_narrow", query, prefix+`,"fields":["sales"]}`, 400, "")
	request("mlc_narrow", query, prefix+`,"fields":["_id"],"filters":{"sales":42}}`, 400, "")
	request("mlc_narrow", query, prefix+`,"fields":["_id"],"pipeline":[{"$match":{"sales":42}}]}`, 400, "")
	request("mlc_wide", query, prefix+`,"fields":["_id"],"filters":{"sales":42}}`, 200, "MISS")
	request("mlc_wide", query, prefix+`,"fields":["_id"],"filters":{"sales":42}}`, 200, "HIT")
	// Shrink the live allowlist without rotating the source. Existing grants,
	// discovery, schema, and warm cached rows must all respect the joined source.
	srv.connections["demo"].databases["analytics"] = []string{"orders"}
	request("mlc_wide", query, ids, 404, "")
	request("mlc_wide", "/api/v1/schema", prefix+"}", 404, "")
	if got := string(request("mlc_wide", "/api/v1/connections", "", 200, "")); got != `{"connections":[]}` {
		t.Fatal(got)
	}
	if _, err := srv.datasetGrant(context.Background(), scope{DatasetID: d.ID, Fields: []string{"_id"}}); err != control.ErrNotFound {
		t.Errorf("could grant unavailable joined dataset: %v", err)
	}
	srv.connections["demo"].databases["analytics"] = []string{"orders", "zones"}
	delete(store.tokens, "mlc_wide")
	request("mlc_wide", query, ids, 401, "")
	store.dataset.Revision = "v2"
	request("mlc_narrow", query, ids, 404, "")
	if got := string(request("mlc_narrow", "/api/v1/connections", "", 200, "")); got != `{"connections":[]}` {
		t.Fatal(got)
	}
	store.dataset.Revision = "v1"
	srv.connections["demo"].databases["analytics"] = []string{"other"}
	request("mlc_narrow", query, ids, 404, "")
	delete(srv.connections, "demo")
	request("mlc_narrow", query, ids, 404, "")
}

func TestDatasetCacheCapsEntriesAndBytesAndExpiresOnRead(t *testing.T) {
	var c resultCache
	for i := 0; i < cacheMaxEntries+1; i++ {
		c.put([32]byte{byte(i)}, []byte{1})
	}
	if len(c.entries) != cacheMaxEntries || c.bytes != cacheMaxEntries {
		t.Fatal("entry cap failed")
	}
	key := [32]byte{42}
	c.put(key, make([]byte, cacheMaxBytes))
	if len(c.entries) != 1 || c.bytes != cacheMaxBytes {
		t.Fatal("byte cap failed")
	}
	c.put(key, []byte{1, 2})
	end := c.entries[key].expires
	if !slices.Equal(c.get(key), []byte{1, 2}) || c.entries[key].expires != end || c.bytes != 2 {
		t.Fatal("replacement or fixed expiry failed")
	}
	c.entries[key] = cacheEntry{[]byte{1, 2}, time.Now().Add(-time.Second)}
	if c.get(key) != nil || c.bytes != 0 {
		t.Fatal("expired entry served")
	}
}
