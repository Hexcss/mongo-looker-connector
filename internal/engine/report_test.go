package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func cursor(t *testing.T, docs ...any) *mongo.Cursor {
	t.Helper()
	c, err := mongo.NewCursorFromDocuments(docs, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSchemaMergesDottedPathsAndInfersTypes(t *testing.T) {
	docs := []any{
		bson.D{{Key: "name", Value: "north"}, {Key: "qty", Value: nil}, {Key: "mixed", Value: 1}, {Key: "nested", Value: bson.M{"a": true}}, {Key: "nested.a", Value: "literal"}},
		bson.M{"qty": int64(12), "mixed": false, "empty": nil, "at": time.Unix(0, 0), "tags": bson.A{"a"}},
	}
	got, err := readSchema(context.Background(), cursor(t, docs...))
	want := []Field{{"at", "YEAR_MONTH_DAY_SECOND"}, {"empty", "TEXT"}, {"mixed", "TEXT"}, {"name", "TEXT"}, {"nested.a", "BOOLEAN"}, {`nested\.a`, "TEXT"}, {"qty", "NUMBER"}, {"tags", "TEXT"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("schema %#v, %v", got, err)
	}
}

func TestRowsPreserveFieldOrderNullsAndPrecision(t *testing.T) {
	id, _ := bson.ObjectIDFromHex("65586bdf1151b79df6f8fbe0")
	d, err := bson.ParseDecimal128("100.00100")
	if err != nil {
		t.Fatal(err)
	}
	fields := []string{"nested.a", "absent", "id", "small", "large", "negative", "decimal", "date", "mixed", "tags", "nan"}
	docs := []any{
		bson.M{"nested": bson.M{"a": true}, "id": id, "small": int64(9007199254740991), "large": int64(9007199254740992), "negative": int64(-9007199254740992), "decimal": d, "date": time.Date(2024, 2, 3, 4, 5, 6, 999000000, time.UTC), "mixed": 42, "tags": bson.A{"a"}, "nan": math.NaN()},
		bson.M{"mixed": "late"},
	}
	result, err := readQuery(context.Background(), cursor(t, docs...), fields)
	want := [][]any{{true, nil, id.Hex(), int64(9007199254740991), "9007199254740992", "-9007199254740992", "100.00100", "20240203040506", "42", `["a"]`, "NaN"}, {nil, nil, nil, nil, nil, nil, nil, nil, "late", nil, nil}}
	if err != nil || !reflect.DeepEqual(result.Fields, fields) || !reflect.DeepEqual(result.Rows, want) {
		t.Fatalf("rows %#v, %v", result, err)
	}
}

func TestReadQueryRefusesExtraRowsAndOversizedResults(t *testing.T) {
	docs := make([]any, 1001)
	for i := range docs {
		docs[i] = bson.M{"a": "x"}
	}
	// Escaped output grows beyond 2 MiB while the input documents stay small.
	large := bson.M{"a": strings.Repeat("<", 80000)}
	for _, tc := range [][]any{docs, {large, large, large, large, large}} {
		if r, err := readQuery(context.Background(), cursor(t, tc...), []string{"a"}); !errors.Is(err, ErrLimit) || len(r.Rows) > 0 {
			t.Fatalf("expected bounded error, rows=%d err=%v", len(r.Rows), err)
		}
	}
}

func TestNullOnlyAndDriftingTypesAfterSchemaSample(t *testing.T) {
	docs := make([]any, 101)
	for i := range docs {
		docs[i] = bson.M{"a": true, "b": nil}
	}
	docs[100] = bson.M{"a": "changed", "b": 5}
	if _, err := readQuery(context.Background(), cursor(t, docs...), []string{"a"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("late type conflict: %v", err)
	}
	r, err := readQuery(context.Background(), cursor(t, docs...), []string{"b"})
	if err != nil || len(r.Rows) != 101 || r.Rows[100][0] != "5" {
		t.Fatalf("null-only sample: %#v %v", r, err)
	}
}

func TestInvalidRequestsRejectedBeforeDatabase(t *testing.T) {
	for _, fields := range [][]string{nil, {}, {"x", "x"}, {""}, {strings.Repeat("x", 513)}} {
		if _, err := Query(context.Background(), nil, nil, fields); !errors.Is(err, ErrInvalid) {
			t.Errorf("expected invalid fields: %v", err)
		}
	}
	if _, err := Schema(context.Background(), nil, []byte(`[{"$merge":"output"}]`)); !errors.Is(err, ErrInvalid) {
		t.Errorf("expected invalid schema query: %v", err)
	}
}
