package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/drivertest"
)

func TestCollectionDiscoveryCommands(t *testing.T) {
	success := bson.D{{Key: "ok", Value: 1}, {Key: "cursor", Value: bson.M{
		"id": int64(0), "ns": "analytics.$cmd.listCollections",
		"firstBatch": bson.A{bson.M{"name": "private"}, bson.M{"name": "orders"}},
	}}}
	for _, tc := range []struct {
		name      string
		firstCode int32
		status    int
		commands  int
	}{
		{"ordinary discovery", 0, 200, 1},
		{"collection-scoped MongoDB identity", 13, 200, 2},
		{"other server error", 2, 502, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			replies := []bson.D{success}
			if tc.firstCode != 0 {
				denied := bson.D{{Key: "ok", Value: 0}, {Key: "code", Value: tc.firstCode}, {Key: "errmsg", Value: "operation failed"}}
				replies = []bson.D{denied, success}
			}
			var commands []bson.Raw
			opts := options.Client().SetMonitor(&event.CommandMonitor{Started: func(ctx context.Context, evt *event.CommandStartedEvent) {
				if evt.CommandName == "listCollections" {
					commands = append(commands, append(bson.Raw(nil), evt.Command...))
				}
			}})
			opts.Deployment = drivertest.NewMockDeployment(replies...)
			client, err := mongo.Connect(opts)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { client.Disconnect(context.Background()) })
			srv := Server{mux: http.NewServeMux(), connections: map[string]connection{
				"demo": {client: client, databases: map[string][]string{"analytics": {"orders"}}},
			}}
			srv.routes("dev", "unknown")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/collections?connectionId=demo&database=analytics", nil))
			if w.Code != tc.status || len(commands) != tc.commands {
				t.Fatalf("got status %d, commands %d; want %d and %d", w.Code, len(commands), tc.status, tc.commands)
			}
			if w.Code == 200 && w.Body.String() != `{"collections":["orders"]}` {
				t.Fatalf("unexpected visible collections: %s", w.Body.String())
			}
			for i, command := range commands {
				if command.Lookup("maxTimeMS").Type != 0 || !command.Lookup("nameOnly").Boolean() {
					t.Fatalf("unexpected discovery command: %v", command)
				}
				auth := command.Lookup("authorizedCollections")
				if (i == 0 && auth.Type != 0) || (i == 1 && (auth.Type != bson.TypeBoolean || !auth.Boolean())) {
					t.Fatalf("unexpected authorizedCollections in command %d: %v", i, command)
				}
			}
		})
	}
}
