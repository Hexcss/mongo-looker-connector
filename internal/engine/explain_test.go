package engine

import (
	"context"
	"encoding/json"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/drivertest"
)

func explainJSON(t *testing.T, plan string) string {
	t.Helper()
	var raw bson.Raw
	if err := bson.UnmarshalExtJSON([]byte(plan), false, &raw); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(summarizeExplain(raw))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestExplainSummarizesExecutedWork(t *testing.T) {
	for _, tc := range []struct{ name, plan, want string }{
		{"native nested loop totals counted once", `{"executionStats":{"totalDocsExamined":8,"totalKeysExamined":4,"executionStages":{"stage":"nlj","indexesUsed":["customer_1"],"innerStage":{"stage":"ixseek","indexName":"customer_1","docsExamined":1000},"outerStage":{"stage":"filter","inputStage":{"stage":"ixseek","indexName":"amount_1"}}},"allPlansExecution":[{"totalDocsExamined":90,"indexName":"other_1"}]},"queryPlanner":{"rejectedPlans":[{"stage":"IXSCAN","indexName":"other_1"}]}}`, `{"documentsExamined":8,"keysExamined":4,"indexesUsed":["amount_1","customer_1"],"rowLimit":50}`},
		{"classic cursor plus lookup", `{"stages":[{"$cursor":{"executionStats":{"totalDocsExamined":4,"totalKeysExamined":1,"executionStages":{"stage":"FETCH","inputStage":{"stage":"IXSCAN","indexName":"order_1"}}}}},{"$lookup":{"from":"customers"},"totalDocsExamined":2,"totalKeysExamined":2,"indexesUsed":["_id_"]},{"$project":{"totalDocsExamined":100},"nReturned":2}]}`, `{"documentsExamined":6,"keysExamined":3,"indexesUsed":["_id_","order_1"],"rowLimit":50}`},
		{"separate shard partitions", `{"shards":{"a":{"executionStats":{"totalDocsExamined":3,"totalKeysExamined":0,"executionStages":{"stage":"COLLSCAN"}}},"b":{"executionStats":{"totalDocsExamined":2,"totalKeysExamined":1,"executionStages":{"stage":"IXSCAN","indexName":"test_1"}}}}}`, `{"documentsExamined":5,"keysExamined":1,"indexesUsed":["test_1"],"rowLimit":50}`},
		{"Firestore collection scan lacks key counter", `{"queryPlanner":{"namespace":"analytics.orders"},"executionStats":{"totalDocsExamined":2,"executionStages":{"stage":"COMPUTE","outputBindings":{"indexName":"private"},"inputStage":{"stage":"COLLSCAN","docsExamined":2,"filter":{"indexName":"private"}}}}}`, `{"documentsExamined":2,"keysExamined":null,"indexesUsed":[],"rowLimit":50}`},
		{"Firestore joined indexed execution", `{"executionStats":{"totalDocsExamined":2,"totalKeysExamined":1,"executionStages":{"stage":"APPLY","inputStages":[{"stage":"COMPUTE","inputStage":{"stage":"IDXSCAN","index":"**/orders (amount ASC)@[id = CAE]","keysExamined":1}},{"stage":"FETCH","outputBindings":{"index":"private"}}]}}}`, `{"documentsExamined":2,"keysExamined":1,"indexesUsed":["**/orders (amount ASC)@[id = CAE]"],"rowLimit":50}`},
		{"no statistics available", `{"command":{"indexesUsed":["user data"]},"queryPlanner":{"rejectedPlans":[{"indexName":"private"}]}}`, `{"documentsExamined":null,"keysExamined":null,"indexesUsed":null,"rowLimit":50}`},
		{"missing shard metrics are unknown", `{"shards":{"a":{"executionStats":{"totalDocsExamined":8,"totalKeysExamined":3,"executionStages":{"stage":"IXSCAN","indexName":"amount_1"}}},"b":{"queryPlanner":{}}}}`, `{"documentsExamined":null,"keysExamined":null,"indexesUsed":null,"rowLimit":50}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := explainJSON(t, tc.plan); got != tc.want {
				t.Errorf("stats %s, want %s", got, tc.want)
			}
		})
	}
}

func TestExplainUsesBoundedAggregate(t *testing.T) {
	var commands []bson.Raw
	opts := options.Client().SetMonitor(&event.CommandMonitor{Started: func(ctx context.Context, e *event.CommandStartedEvent) {
		commands = append(commands, append(bson.Raw(nil), e.Command...))
	}})
	opts.Deployment = drivertest.NewMockDeployment(bson.D{{Key: "ok", Value: 1}})
	client, err := mongo.Connect(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	coll := client.Database("analytics").Collection("orders")
	if _, err = Explain(context.Background(), coll, json.RawMessage(`[{"$out":"archive"}]`), nil); err == nil || len(commands) != 0 {
		t.Fatal("invalid pipeline sent to database")
	}
	if _, err = Explain(context.Background(), coll, json.RawMessage(`[{"$match":{"amount":42}}]`), nil); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 {
		t.Fatalf("sent %d commands, want one", len(commands))
	}
	run := commands[0]
	aggregate := document(run, "explain")
	if aggregate.Lookup("aggregate").StringValue() != "orders" || aggregate.Lookup("allowDiskUse").Boolean() || run.Lookup("verbosity").StringValue() != "executionStats" {
		t.Fatal("expected read-only aggregate execution stats")
	}
	stages := values(aggregate, "pipeline")
	if len(stages) != 2 || stages[1].Document().Lookup("$limit").AsInt64() != previewRows {
		t.Fatal("missing preview bound")
	}
	// The driver applies the context deadline to the wire command as well.
	maxTime := counter(run, "maxTimeMS")
	if maxTime == nil || *maxTime <= 0 || *maxTime > queryTimeout.Milliseconds()+1 {
		t.Fatal("missing operation deadline")
	}
}
