package engine

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestPipelineKeepsSortPriorityAndAppendsServerLimit(t *testing.T) {
	stages, err := buildPipeline(json.RawMessage(`[{"$match":{"qty":{"$gt":0}}},{"$sort":{"z":-1,"a":1}},{"$group":{"_id":"$region","total":{"$sum":"$qty"}}}]`), 101)
	if err != nil {
		t.Fatal(err)
	}
	want := bson.D{{Key: "z", Value: int64(-1)}, {Key: "a", Value: int64(1)}}
	if len(stages) != 4 || !reflect.DeepEqual(stages[1][0].Value, want) || !reflect.DeepEqual(stages[3], bson.D{{Key: "$limit", Value: int64(101)}}) {
		t.Fatalf("unexpected ordered stages: %#v", stages)
	}
	for _, empty := range []string{"", " ", "[]"} {
		p, err := buildPipeline(json.RawMessage(empty), 101)
		if err != nil || len(p) != 1 {
			t.Errorf("default pipeline %q: %v, %v", empty, p, err)
		}
	}
}

func TestPipelineRejectsUnsupportedStructureAtAnyDepth(t *testing.T) {
	for _, input := range []string{
		`{}`, `null`, `[null]`, `[{}]`, `[] []`,
		`[{"$match":{},"$sort":{}}]`, `[{"$match":{"x":1,"x":2}}]`,
		`[{"$out":"elsewhere"}]`, `[{"$merge":"elsewhere"}]`, `[{"$lookup":{}}]`, `[{"$unionWith":"elsewhere"}]`,
		`[{"$match":{"$and":[{"x":1},{"$where":"predicate"}]}}]`,
		`[{"$project":{"x":{"$function":{}}}}]`, `[{"$group":{"a":{"$accumulator":{}}}}]`,
		`[{"$match":{"x":{"$code":"value"}}}]`, `[{"$match":{"x":{"$newOperator":1}}}]`,
		`[{"$match":{"x":"$$USER_ROLES"}}]`, `[{"$match":{"x\u0000y":1}}]`,
		`[{"$match":{"n":9223372036854775808}}]`,
	} {
		if _, err := buildPipeline(json.RawMessage(input), 10); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted invalid pipeline %s: %v", input, err)
		}
	}
}

func TestPipelineBounds(t *testing.T) {
	for _, input := range []string{
		strings.Repeat(" ", 32769),
		"[" + strings.Repeat(`{"$match":{}},`, 32) + `{"$match":{}}]`,
		`[{"$match":{"a":` + strings.Repeat("[", 40) + "1" + strings.Repeat("]", 40) + `}}]`,
	} {
		if _, err := buildPipeline(json.RawMessage(input), 10); !errors.Is(err, ErrLimit) {
			t.Errorf("expected limit: %v", err)
		}
	}
}

func TestEmptyMatchReachesMongoAsDocument(t *testing.T) {
	p, err := buildPipeline(json.RawMessage(`[{"$match":{}}]`), 10)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := bson.Marshal(p[0])
	if err != nil {
		t.Fatal(err)
	}
	if got := bson.Raw(raw).Lookup("$match").Type; got != bson.TypeEmbeddedDocument {
		t.Fatalf("empty match encoded as %s", got)
	}
}

func TestMalformedStageArgumentsRejectedBeforeDatabase(t *testing.T) {
	for _, input := range []string{
		`[{"$match":null}]`, `[{"$project":1}]`, `[{"$group":{"total":{"$sum":1}}}]`,
		`[{"$limit":0}]`, `[{"$skip":-1}]`, `[{"$sort":{"a":0}}]`,
		`[{"$count":1}]`, `[{"$unwind":"notAPath"}]`, `[{"$unset":[1]}]`,
	} {
		if _, err := buildPipeline(json.RawMessage(input), 10); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted malformed stage %s: %v", input, err)
		}
	}
}

func TestPipelineLookupCollections(t *testing.T) {
	raw := json.RawMessage(`[{"$lookup":{"as":"shipping.zone","from":"shipping_zones","foreignField":"region.code","localField":"address.region"}},{"$unwind":"$shipping.zone"},{"$lookup":{"from":"accounts","localField":"accountId","foreignField":"_id","as":"account"}}]`)
	got, err := LookupCollections(raw)
	if err != nil || !reflect.DeepEqual(got, []string{"shipping_zones", "accounts"}) {
		t.Fatalf("lookup dependencies %v: %v", got, err)
	}
	for _, input := range []string{
		`{"from":"zones","localField":"region","foreignField":"_id"}`,
		`{"from":{"db":"other","coll":"zones"},"localField":"region","foreignField":"_id","as":"zone"}`,
		`{"from":"zones","localField":"region","foreignField":"_id","as":"zone","pipeline":[]}`,
		`{"from":"zones","localField":"region","foreignField":"_id","as":"zone","let":{}}`,
		`{"from":"zones","localField":"region","foreignField":"_id","as":"zone","as":"other"}`,
		`{"from":"zones","localField":"region","foreignField":"$name","as":"zone"}`,
		`{"from":"zones","localField":"a..b","foreignField":"_id","as":"zone"}`,
		`{"from":"zones","localField":"region","foreignField":"_id","as":""}`,
		`{"from":"zones","localField":"region","foreignField":"_id","as":1}`,
		`{"from":"","localField":"region","foreignField":"_id","as":"zone"}`,
	} {
		if _, err := LookupCollections(json.RawMessage(`[{"$lookup":` + input + `}]`)); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted invalid lookup %s (%v)", input, err)
		}
	}
}
