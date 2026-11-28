package engine

import (
	"context"
	"encoding/json"
	"math"
	"slices"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

// ExplainStats describes the bounded preview operation. A nil metric means that
// the database did not report it, whereas a measured zero is meaningful.
type ExplainStats struct {
	DocumentsExamined *int64   `json:"documentsExamined"`
	KeysExamined      *int64   `json:"keysExamined"`
	IndexesUsed       []string `json:"indexesUsed"`
	RowLimit          int      `json:"rowLimit"`
}

// Explain uses the same validated aggregation and result cap as Preview. The
// gateway authorizes all collection dependencies before opening the source.
func Explain(ctx context.Context, coll *mongo.Collection, raw json.RawMessage, pref *readpref.ReadPref) (ExplainStats, error) {
	pipeline, err := buildPipeline(raw, previewRows)
	if err != nil {
		return ExplainStats{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	command := bson.D{
		{Key: "explain", Value: bson.D{
			{Key: "aggregate", Value: coll.Name()}, {Key: "pipeline", Value: pipeline},
			{Key: "cursor", Value: bson.D{}}, {Key: "allowDiskUse", Value: false},
		}},
		{Key: "verbosity", Value: "executionStats"},
	}
	// RunCommand defaults to primary irrespective of the source configuration.
	opts := options.RunCmd()
	if pref != nil {
		opts.SetReadPreference(pref)
	}
	result, err := coll.Database().RunCommand(ctx, command, opts).Raw()
	if err != nil {
		return ExplainStats{}, err
	}
	return summarizeExplain(result), nil
}

func document(r bson.Raw, key string) bson.Raw {
	d, _ := r.Lookup(key).DocumentOK()
	return d
}

func values(r bson.Raw, key string) []bson.RawValue {
	a, ok := r.Lookup(key).ArrayOK()
	if !ok {
		return nil
	}
	items, _ := a.Values()
	return items
}

func counter(r bson.Raw, key string) *int64 {
	v := r.Lookup(key)
	switch v.Type {
	case bson.TypeInt32, bson.TypeInt64:
		n, ok := v.AsInt64OK()
		if ok && n >= 0 {
			return &n
		}
	case bson.TypeDouble:
		d := v.Double()
		if d >= 0 && d < math.MaxInt64 && d == math.Trunc(d) {
			n := int64(d)
			return &n
		}
	}
	return nil
}

func addCounters(a, b *int64) *int64 {
	if a == nil || b == nil || *a > math.MaxInt64-*b {
		return nil
	}
	n := *a + *b
	return &n
}

func zeroStats() ExplainStats {
	var n int64
	return ExplainStats{DocumentsExamined: &n, KeysExamined: &n, IndexesUsed: []string{}, RowLimit: previewRows}
}

func (s *ExplainStats) add(part ExplainStats) {
	s.DocumentsExamined = addCounters(s.DocumentsExamined, part.DocumentsExamined)
	s.KeysExamined = addCounters(s.KeysExamined, part.KeysExamined)
	if s.IndexesUsed == nil || part.IndexesUsed == nil {
		s.IndexesUsed = nil
	} else {
		s.IndexesUsed = append(s.IndexesUsed, part.IndexesUsed...)
	}
}

func indexList(r bson.Raw) []string {
	if _, ok := r.Lookup("indexesUsed").ArrayOK(); !ok {
		return nil
	}
	names := []string{}
	for _, v := range values(r, "indexesUsed") {
		name, ok := v.StringValueOK()
		if !ok || name == "" {
			return nil
		}
		names = append(names, name)
	}
	return names
}

// Walk only named plan edges. Filters, projections and rejected candidates can
// contain identically named properties and are never execution evidence.
func executedIndexes(node bson.Raw, names *[]string) bool {
	if node == nil {
		return false
	}
	stage, _ := node.Lookup("stage").StringValueOK()
	known := stage == "COLLSCAN" || stage == "scan" || stage == "EOF"
	indexField := "indexName"
	// Firestore's Mongo API uses an index description on the execution node.
	if stage == "IDXSCAN" || stage == "IDXSEEK" {
		indexField = "index"
	}
	if name, ok := node.Lookup(indexField).StringValueOK(); ok && name != "" {
		*names = append(*names, name)
		known = true
	}
	if list := indexList(node); list != nil {
		*names = append(*names, list...)
		known = true
	}
	for _, key := range []string{"inputStage", "outerStage", "innerStage", "thenStage", "elseStage", "queryPlan", "winningPlan", "executionStages"} {
		if child := document(node, key); child != nil && executedIndexes(child, names) {
			known = true
		}
	}
	for _, key := range []string{"inputStages", "shards", "children", "branches"} {
		for _, v := range values(node, key) {
			if child, ok := v.DocumentOK(); ok && executedIndexes(child, names) {
				known = true
			}
		}
	}
	return known
}

func summarizeExplain(r bson.Raw) ExplainStats {
	result := explainWork(r)
	slices.Sort(result.IndexesUsed)
	result.IndexesUsed = slices.Compact(result.IndexesUsed)
	return result
}

func explainWork(r bson.Raw) ExplainStats {
	stats := ExplainStats{RowLimit: previewRows}
	if execution := document(r, "executionStats"); execution != nil {
		stats.DocumentsExamined = counter(execution, "totalDocsExamined")
		stats.KeysExamined = counter(execution, "totalKeysExamined")
		names := []string{}
		known := executedIndexes(document(execution, "executionStages"), &names)
		// With the slot-based engine the physical index chosen for an EQ_LOOKUP
		// may appear only in the winning logical plan.
		if executedIndexes(document(document(r, "queryPlanner"), "winningPlan"), &names) || known {
			stats.IndexesUsed = names
		}
		return stats
	}
	if shards := document(r, "shards"); shards != nil {
		// A merge-side lookup reads the foreign collection after shard-local
		// execution. The split pipeline is a description, with no counters.
		// Without coordinator totals the shard plans cannot describe this work.
		for _, value := range values(document(r, "splitPipeline"), "mergerPart") {
			stage, _ := value.DocumentOK()
			if document(stage, "$lookup") != nil {
				return stats
			}
		}
		entries, _ := shards.Elements()
		if len(entries) == 0 {
			return stats
		}
		total := zeroStats()
		for _, e := range entries {
			shard, _ := e.Value().DocumentOK()
			total.add(explainWork(shard))
		}
		return total
	}
	// Classic aggregations put the local cursor and each foreign collection's
	// work in separate stages. Their totals are disjoint. Counters inside a
	// single executionStages tree describe overlapping work and must not be added.
	total := zeroStats()
	found := false
	for _, v := range values(r, "stages") {
		stage, _ := v.DocumentOK()
		if cursor := document(stage, "$cursor"); cursor != nil {
			total.add(explainWork(cursor))
			found = true
		} else if document(stage, "$lookup") != nil {
			total.add(ExplainStats{DocumentsExamined: counter(stage, "totalDocsExamined"), KeysExamined: counter(stage, "totalKeysExamined"), IndexesUsed: indexList(stage)})
			found = true
		}
	}
	if found {
		return total
	}
	return stats
}
