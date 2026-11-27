package engine

import (
	"context"
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/drivertest"
)

func TestPreviewBoundsOutputAndInfersColumns(t *testing.T) {
	reply := bson.D{{Key: "ok", Value: 1}, {Key: "cursor", Value: bson.M{"id": int64(0), "ns": "test.orders", "firstBatch": bson.A{bson.M{"amount": 42}}}}}
	var limits []int64
	opts := options.Client().SetMonitor(&event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if e.CommandName == "aggregate" {
			stages, err := e.Command.Lookup("pipeline").Array().Values()
			if err != nil || len(stages) == 0 {
				t.Fatal("expected bounded aggregation")
			}
			limits = append(limits, stages[len(stages)-1].Document().Lookup("$limit").AsInt64())
		}
	}})
	opts.Deployment = drivertest.NewMockDeployment(reply, reply)
	client, err := mongo.Connect(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	result, err := Preview(t.Context(), client.Database("test").Collection("orders"), nil, nil)
	if err != nil || !reflect.DeepEqual(result, Result{Fields: []string{"amount"}, Rows: [][]any{{int64(42)}}}) {
		t.Fatalf("unexpected preview %#v, %v", result, err)
	}
	if limits[len(limits)-1] != 50 {
		t.Fatalf("unbounded preview: %v", limits)
	}
}
