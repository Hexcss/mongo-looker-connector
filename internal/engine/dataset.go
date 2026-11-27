package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func validDatasetPath(name string) bool {
	if len(name) > maxNameBytes || !utf8.ValidString(name) || strings.ContainsAny(name, "\\\x00") {
		return false
	}
	for _, part := range strings.Split(name, ".") {
		if part == "" || part != strings.TrimSpace(part) || strings.HasPrefix(part, "$") {
			return false
		}
	}
	return true
}

func datasetTypes(schema []Field) (map[string]string, error) {
	if len(schema) == 0 || len(schema) > maxFields {
		return nil, ErrInvalid
	}
	types := make(map[string]string, len(schema))
	for _, f := range schema {
		if types[f.Name] != "" || !validDatasetPath(f.Name) {
			return nil, ErrInvalid
		}
		switch f.Type {
		case "TEXT", "NUMBER", "BOOLEAN", "YEAR_MONTH_DAY_SECOND":
			types[f.Name] = f.Type
		default:
			return nil, ErrInvalid
		}
	}
	for name := range types {
		for i := range name {
			if name[i] == '.' && types[name[:i]] != "" {
				return nil, ErrInvalid
			}
		}
	}
	return types, nil
}

func ValidateDataset(pipeline json.RawMessage, schema []Field) error {
	if _, err := datasetTypes(schema); err != nil {
		return err
	}
	_, err := parsePipeline(pipeline)
	return err
}

// Filters are ANDed exact BSON scalar comparisons on the published pipeline output.
// Decode each literal against the declared type; arbitrary MongoDB expressions cannot enter a predicate.
func datasetFilter(kind string, raw json.RawMessage) (any, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, ErrInvalid
	}
	switch kind {
	case "NUMBER":
		var n float64
		if err := json.Unmarshal(raw, &n); err == nil && n >= -(1<<53-1) && n <= 1<<53-1 {
			return n, nil
		}
	case "BOOLEAN":
		var b bool
		if err := json.Unmarshal(raw, &b); err == nil {
			return b, nil
		}
	case "TEXT", "YEAR_MONTH_DAY_SECOND":
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			if kind == "TEXT" {
				return s, nil
			}
			if t, err := time.Parse("20060102150405", s); err == nil && t.Year() > 0 {
				return t, nil
			}
		}
	}
	return nil, ErrInvalid
}

// Plan and validate publication-bound filters once per request before accessing the database.
func datasetSelection(schema []Field, fields []string, dates *DateRange, filters map[string]json.RawMessage) (map[string]string, mongo.Pipeline, error) {
	types, err := datasetTypes(schema)
	if err != nil {
		return nil, nil, err
	}
	if len(fields) == 0 || len(fields) > len(schema) || len(filters) > len(schema) {
		return nil, nil, ErrInvalid
	}
	seen := map[string]bool{}
	for _, name := range fields {
		if types[name] == "" || seen[name] {
			return nil, nil, ErrInvalid
		}
		seen[name] = true
	}

	var stages mongo.Pipeline
	var terms bson.A
	if dates != nil {
		if types[dates.Field] != "YEAR_MONTH_DAY_SECOND" {
			return nil, nil, ErrInvalid
		}
		match, err := dates.match()
		if err != nil {
			return nil, nil, err
		}
		stages = append(stages, match)
		// A range predicate also matches elements of arrays in MongoDB. Only a
		// scalar BSON date participates in a published date control.
		terms = append(terms, bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: "$" + dates.Field}}, "date"}}})
	}
	for _, name := range slices.Sorted(maps.Keys(filters)) {
		value, err := datasetFilter(types[name], filters[name])
		if err != nil {
			return nil, nil, err
		}
		field := "$" + name
		// Expression equality compares the complete scalar. A query predicate would
		// also match elements of arrays. Keep strings starting with '$' literal.
		terms = append(terms, bson.D{{Key: "$eq", Value: bson.A{field, bson.D{{Key: "$literal", Value: value}}}}})
		if types[name] == "NUMBER" {
			// Decimal128 is exposed as TEXT even when its value equals a numeric literal.
			terms = append(terms, bson.D{{Key: "$in", Value: bson.A{bson.D{{Key: "$type", Value: field}}, bson.A{"int", "long", "double"}}}})
		}
	}
	if len(terms) > 0 {
		stages = append(stages, bson.D{{Key: "$match", Value: bson.D{{Key: "$expr", Value: bson.D{{Key: "$and", Value: terms}}}}}})
	}
	return types, stages, nil
}

func ValidateDatasetQuery(schema []Field, fields []string, dates *DateRange, filters map[string]json.RawMessage) error {
	_, _, err := datasetSelection(schema, fields, dates, filters)
	return err
}

// DatasetQuery accepts the caller-visible schema. Its output projection uses generated
// keys to preserve dotted paths, distinguish missing values, and exclude all other data.
func DatasetQuery(ctx context.Context, coll *mongo.Collection, pipeline json.RawMessage, schema []Field, fields []string, dates *DateRange, filters map[string]json.RawMessage, sortByID, preview bool) (Result, error) {
	types, selection, err := datasetSelection(schema, fields, dates, filters)
	if err != nil {
		return Result{}, err
	}
	stages, err := parsePipeline(pipeline)
	if err != nil {
		return Result{}, err
	}
	stages = append(stages, selection...)
	if sortByID {
		stages = append(stages, bson.D{{Key: "$sort", Value: bson.D{{Key: "_id", Value: 1}}}})
	}
	limit := maxRows + 1
	if preview {
		limit = 50
	}
	project := bson.D{{Key: "_id", Value: 0}}
	for i, name := range fields {
		project = append(project, bson.E{Key: "c" + strconv.Itoa(i), Value: "$" + name})
	}
	stages = append(stages, bson.D{{Key: "$limit", Value: limit}}, bson.D{{Key: "$project", Value: project}})
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	c, err := coll.Aggregate(ctx, stages, options.Aggregate().SetAllowDiskUse(false).SetBatchSize(32))
	if err != nil {
		return Result{}, err
	}
	defer c.Close(ctx)
	return readDataset(ctx, c, fields, types)
}

func readDataset(ctx context.Context, c *mongo.Cursor, fields []string, types map[string]string) (Result, error) {
	return collectRows(ctx, c, fields, func(_ int, doc bson.Raw) ([]any, error) {
		row := make([]any, len(fields))
		for i, name := range fields {
			v := doc.Lookup("c" + strconv.Itoa(i))
			switch v.Type {
			case 0, bson.TypeNull, bson.TypeUndefined:
				continue
			case bson.TypeString, bson.TypeObjectID, bson.TypeBoolean, bson.TypeInt32, bson.TypeInt64, bson.TypeDouble, bson.TypeDecimal128, bson.TypeDateTime:
				kind, value := cell(v)
				if kind == types[name] && (v.Type != bson.TypeDateTime || kind == "YEAR_MONTH_DAY_SECOND") {
					row[i] = value
					continue
				}
			}
			return nil, fmt.Errorf("%w: value conflicts with published field type", ErrInvalid)
		}
		return row, nil
	})
}
