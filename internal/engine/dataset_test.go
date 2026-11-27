package engine

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/drivertest"
)

func TestDatasetValidatesSchemaAndStages(t *testing.T) {
	fields := []Field{{"profile.name", "TEXT"}, {"profile.age", "NUMBER"}, {"active", "BOOLEAN"}, {"day", "YEAR_MONTH_DAY_SECOND"}}
	if err := ValidateDataset(json.RawMessage(`[{"$sort":{"active":-1,"day":1}}]`), fields); err != nil {
		t.Fatal(err)
	}
	for _, schema := range [][]Field{nil, {{"a.b", "TEXT"}, {"a", "NUMBER"}}, {{"a", "TEXT"}, {"a", "NUMBER"}}, {{"valid", "OBJECT"}}} {
		if err := ValidateDataset(nil, schema); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted invalid schema %v", schema)
		}
	}
	for _, path := range []string{"", "trailing.", ".leading", "a..b", "a.$ref", " spaced", "a\\.b", "a\x00b", strings.Repeat("x", maxNameBytes+1)} {
		if err := ValidateDataset(nil, []Field{{path, "TEXT"}}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted path %q", path)
		}
	}
	for _, pipe := range []string{`[{"$out":"other"}]`, `[{"$lookup":{"from":"other"}}]`, `[{"$sort":{"a":1,"a":-1}}]`} {
		if err := ValidateDataset(json.RawMessage(pipe), fields); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted pipeline %s", pipe)
		}
	}
}

func TestDatasetRequestedFieldsAndPredicateTypes(t *testing.T) {
	schema := []Field{{"name", "TEXT"}, {"amount", "NUMBER"}, {"created", "YEAR_MONTH_DAY_SECOND"}, {"shipped", "BOOLEAN"}}
	for _, fields := range [][]string{nil, {"name", "name"}, {"secret"}, {"name.child"}} {
		if err := ValidateDatasetQuery(schema, fields, nil, nil); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted fields %v", fields)
		}
	}
	selected := []string{"name"}
	for _, tc := range []struct{ field, value string }{
		{"hidden", `"x"`}, {"name", `{"$ne":0}`}, {"name", `[]`}, {"name", `null`},
		{"amount", `"3"`}, {"amount", `9007199254740993`}, {"amount", `-9007199254740992`},
		{"created", `"20260432000000"`}, {"shipped", `"true"`},
	} {
		if err := ValidateDatasetQuery(schema, selected, nil, map[string]json.RawMessage{tc.field: json.RawMessage(tc.value)}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted invalid filter %s=%s", tc.field, tc.value)
		}
	}
	for _, date := range []*DateRange{{"hidden", "2026-03-01", "2026-03-31"}, {"name", "2026-03-01", "2026-03-31"}, {"created", "2026-03-32", "2026-04-01"}} {
		if err := ValidateDatasetQuery(schema, selected, date, nil); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted date range %v", date)
		}
	}
	if err := ValidateDatasetQuery(schema, selected, &DateRange{"created", "2026-03-01", "2026-03-31"}, map[string]json.RawMessage{
		"name": json.RawMessage(`"$other"`), "shipped": json.RawMessage("false"), "amount": json.RawMessage("9007199254740991"), "created": json.RawMessage(`"20260301133000"`),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestManagedAggregateProjectsOnceAfterPublishedStages(t *testing.T) {
	for _, preview := range []bool{true, false} {
		reply := bson.D{{Key: "ok", Value: 1}, {Key: "cursor", Value: bson.M{"id": int64(0), "ns": "test.orders", "firstBatch": bson.A{bson.M{"c0": "value", "c1": 42}}}}}
		var commands []bson.Raw
		opts := options.Client().SetMonitor(&event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
			if e.CommandName == "aggregate" {
				commands = append(commands, append(bson.Raw(nil), e.Command...))
			}
		}})
		opts.Deployment = drivertest.NewMockDeployment(reply)
		client, err := mongo.Connect(opts)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Disconnect(context.Background())
		schema := []Field{{"amount", "NUMBER"}, {"info.label", "TEXT"}, {"visible", "TEXT"}, {"day", "YEAR_MONTH_DAY_SECOND"}}
		fields := []string{"info.label", "amount"}
		result, err := DatasetQuery(t.Context(), client.Database("test").Collection("orders"), json.RawMessage(`[{"$project":{"amount":1,"info":1,"visible":1,"day":1}}]`),
			schema, fields, &DateRange{"day", "2026-01-01", "2026-01-02"}, map[string]json.RawMessage{"visible": json.RawMessage(`"$secret"`), "amount": json.RawMessage("42")}, true, preview)
		if err != nil || !reflect.DeepEqual(result, Result{fields, [][]any{{"value", int64(42)}}}) || len(commands) != 1 {
			t.Fatalf("result %v, %d aggregates, err=%v", result, len(commands), err)
		}
		var p mongo.Pipeline
		if err := commands[0].Lookup("pipeline").Unmarshal(&p); err != nil {
			t.Fatal(err)
		}
		var operations []string
		for _, stage := range p {
			operations = append(operations, stage[0].Key)
		}
		if !reflect.DeepEqual(operations, []string{"$project", "$match", "$match", "$sort", "$limit", "$project"}) {
			t.Fatal(operations)
		}
		rawStages, _ := commands[0].Lookup("pipeline").Array().Values()
		limit := int64(maxRows + 1)
		if preview {
			limit = 50
		}
		if rawStages[4].Document().Lookup("$limit").AsInt64() != limit || commands[0].Lookup("allowDiskUse").Boolean() {
			t.Fatal("query bounds missing")
		}
		expectProject := bson.D{{Key: "_id", Value: int32(0)}, {Key: "c0", Value: "$info.label"}, {Key: "c1", Value: "$amount"}}
		if !reflect.DeepEqual(p[5][0].Value, expectProject) {
			t.Fatal("unexpected output projection", p[5])
		}
		expr, err := rawStages[2].Document().Lookup("$match", "$expr", "$and").Array().Values()
		if err != nil || len(expr) != 4 {
			t.Fatal("invalid predicate terms", err)
		}
		var equality bson.A
		if err := expr[3].Document().Lookup("$eq").Unmarshal(&equality); err != nil || !reflect.DeepEqual(equality, bson.A{"$visible", bson.D{{Key: "$literal", Value: "$secret"}}}) {
			t.Fatalf("literal equality %v: %v", equality, err)
		}
		if expr[0].Document().Lookup("$eq").Array().Index(0).Document().Lookup("$type").StringValue() != "$day" ||
			expr[2].Document().Lookup("$in").Array().Index(0).Document().Lookup("$type").StringValue() != "$amount" {
			t.Fatal("scalar type guard missing")
		}
	}
}

func TestPublishedRowsStayTypedAndNeverStringifyContainers(t *testing.T) {
	fields := []string{"day", "amount", "active", "name"}
	types := map[string]string{"day": "YEAR_MONTH_DAY_SECOND", "amount": "NUMBER", "active": "BOOLEAN", "name": "TEXT"}
	d := time.Date(2026, 3, 1, 18, 13, 27, 0, time.UTC)
	got, err := readDataset(t.Context(), cursor(t, bson.M{"c0": d, "c1": 12, "c2": true, "c3": "ok"}, bson.M{"c1": nil}), fields, types)
	want := Result{fields, [][]any{{"20260301181327", int64(12), true, "ok"}, {nil, nil, nil, nil}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v, %v", got, err)
	}
	for _, v := range []any{int32(2), time.Now(), bson.D{}, bson.M{"child": "private"}, bson.A{"private"}} {
		got, err := readDataset(t.Context(), cursor(t, bson.M{"c0": "ok"}, bson.M{"c0": v}), []string{"name"}, types)
		if !errors.Is(err, ErrInvalid) || len(got.Rows) != 0 {
			t.Fatalf("drifted row returned %v, %v", got, err)
		}
	}
	docs := make([]any, maxRows+1)
	for i := range docs {
		docs[i] = bson.M{"c0": "ok"}
	}
	got, err = readDataset(t.Context(), cursor(t, docs...), []string{"name"}, types)
	if !errors.Is(err, ErrLimit) || len(got.Rows) != 0 {
		t.Fatalf("row limit: %v", err)
	}
}
