package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hexcss/mongo-looker-connector/internal/control"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/drivertest"
)

func TestJoinedPipelineHonorsAllRawGrantsAndConfiguredCollections(t *testing.T) {
	t.Setenv("MLC_LOOKER_CONNECTOR_ID", "sample-deployment")
	t.Setenv("MLC_LOOKER_GATEWAY_ORIGIN", "https://gateway.example")
	batch := bson.D{{Key: "ok", Value: 1}, {Key: "cursor", Value: bson.M{"id": int64(0), "ns": "sales.orders", "firstBatch": bson.A{bson.M{"region": "north"}}}}}
	opts := options.Client()
	opts.Deployment = drivertest.NewMockDeployment(batch, batch)
	db, err := mongo.Connect(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Disconnect(context.Background())
	s := Server{mux: http.NewServeMux(), connections: map[string]connection{"demo": {generation: "v1", client: db, databases: map[string][]string{"sales": {"orders", "zones"}, "archive": {"zones"}}}}}
	s.routes("dev", "fixture")
	base := control.Grant{ConnectionID: "demo", Database: "sales", Collection: "orders", Generation: "v1"}
	foreign := base
	foreign.Collection = "zones"
	otherDB := foreign
	otherDB.Database = "archive"
	stale := foreign
	stale.Generation = "old"
	store := &workspaceCatalog{datasetTestCatalog: datasetTestCatalog{tokens: map[string]control.Token{
		"mlc_partial": {Grants: []control.Grant{base, otherDB, stale}},
		"mlc_both":    {Grants: []control.Grant{base, foreign}},
	}}}
	s.EnableAdmin(store, func(_ context.Context, p string) bool { return p == "owner" }, http.NotFoundHandler())
	request := func(path, credential, body string, want int) string {
		t.Helper()
		r := httptest.NewRequest("POST", "/api/v1/"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if credential == "owner" {
			r.Header.Set("X-MLC-Admin-Assertion", credential)
			r.Header.Set("X-MLC-Request", "console")
		} else {
			r.Header.Set("X-MLC-Token", credential)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s status %d, want %d (%s)", path, credential, w.Code, want, w.Body.String())
		}
		return w.Body.String()
	}
	const selected = `"connectionId":"demo","database":"sales","collection":"orders","pipeline":[{"$lookup":{"from":"zones","foreignField":"_id","localField":"_id","as":"zone"}},{"$unwind":"$zone"},{"$project":{"region":"$zone.region"}}]`
	const query = "{" + selected + `,"fields":["region"]}`
	request("schema", "mlc_partial", "{"+selected+"}", 404)
	request("query", "mlc_partial", query, 404)
	for _, path := range []string{"admin/preview", "admin/workspace/preview", "admin/workspace/explain", "admin/workspace/open", "admin/datasets"} {
		in := strings.Replace(selected, `"from":"zones"`, `"from":"hidden"`, 1)
		switch path {
		case "admin/workspace/open":
			in += `,"id":"daily"`
		case "admin/datasets":
			in += `,"id":"daily","fields":[{"name":"region","type":"TEXT"}]`
		}
		request(path, "owner", "{"+in+"}", 404)
	}
	if got := request("query", "mlc_both", query, 200); got != `{"fields":["region"],"rows":[["north"]]}` {
		t.Fatalf("joined query: %s", got)
	}
	if got := request("schema", "mlc_both", "{"+selected+"}", 200); got != `{"fields":[{"name":"region","type":"TEXT"}]}` {
		t.Fatalf("joined schema: %s", got)
	}
}
