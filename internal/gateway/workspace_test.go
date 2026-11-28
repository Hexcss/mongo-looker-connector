package gateway

import (
	"context"
	"encoding/json"
	"github.com/Hexcss/mongo-looker-connector/internal/control"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/drivertest"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

type workspaceCatalog struct{ datasetTestCatalog }

func (c *workspaceCatalog) SaveDataset(_ context.Context, d control.Dataset) (control.Dataset, error) {
	if d.ID == c.dataset.ID {
		return d, control.ErrConflict
	}
	c.dataset = d
	return d, nil
}
func TestPublishWorkspaceSelection(t *testing.T) {
	t.Setenv("MLC_LOOKER_CONNECTOR_ID", "example-deployment")
	t.Setenv("MLC_LOOKER_GATEWAY_ORIGIN", "https://gateway.example")
	batch := func(doc bson.M) bson.D {
		return bson.D{{Key: "ok", Value: 1}, {Key: "cursor", Value: bson.M{"id": int64(0), "ns": "sales.orders", "firstBatch": bson.A{doc}}}}
	}
	raw, projected := batch(bson.M{"amount": 42}), batch(bson.M{"c0": 42})
	opts := options.Client()
	opts.Deployment = drivertest.NewMockDeployment(raw, raw, raw, projected, raw, projected)
	client, err := mongo.Connect(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	s := Server{mux: http.NewServeMux(), connections: map[string]connection{"demo": {client: client, generation: "source", databases: map[string][]string{"sales": {"orders"}}}}}
	s.routes("dev", "test")
	c := new(workspaceCatalog)
	s.EnableAdmin(c, func(context.Context, string) bool { return true }, http.NotFoundHandler())
	call := func(method, path, body string, code int) string {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1/admin/workspace/"+path, strings.NewReader(body))
		r.Header.Set("X-MLC-Admin-Assertion", "operator")
		r.Header.Set("X-MLC-Request", "console")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != code {
			t.Fatalf("status %d, want %d: %s", w.Code, code, w.Body.String())
		}
		return w.Body.String()
	}
	const data = `"connectionId":"demo","database":"sales","collection":"orders","pipeline":[{"$limit":1}]`
	preview := call("POST", "preview", "{"+data+"}", 200)
	if preview != `{"fields":[{"name":"amount","type":"NUMBER"}],"rows":[[42]]}` {
		t.Fatal(preview)
	}
	body := `{"id":"orders-daily",` + data + "}"
	result := call("POST", "open", body, 201)
	if c.dataset.SourceGeneration != "source" || string(c.dataset.Pipeline) != `[{"$limit":1}]` {
		t.Fatal("lost pipeline or source generation")
	}
	if call("GET", "looker/orders-daily", "", 200) != result {
		t.Fatal("link changed")
	}
	var handoff map[string]string
	if err := json.Unmarshal([]byte(result), &handoff); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(handoff["url"])
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]string
	if err := json.Unmarshal([]byte(u.Query().Get("connectorConfig")), &cfg); err != nil {
		t.Fatal(err)
	}
	if u.Host != "lookerstudio.google.com" || u.Query().Get("connectorId") != "example-deployment" || !reflect.DeepEqual(cfg, map[string]string{"collection": "orders-daily", "connectionId": "%40datasets", "gatewayOrigin": "https%3A%2F%2Fgateway.example"}) {
		t.Fatal(handoff)
	}
	call("POST", "open", body, 409)
	call("POST", "preview", strings.Replace("{"+data+"}", "orders", "private", 1), 404)
}
