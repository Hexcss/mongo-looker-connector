package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	sampleRows     = 100
	maxRows        = 1000
	maxFields      = 256
	maxNameBytes   = 512
	maxResultBytes = 2 << 20
	queryTimeout   = 10 * time.Second
)

type Field struct {
	Name string `json:"name"`
	Type string `json:"type"`
}
type Result struct {
	Fields []string `json:"fields"`
	Rows   [][]any  `json:"rows"`
}

func Schema(ctx context.Context, coll *mongo.Collection, pipeline json.RawMessage) ([]Field, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	cur, err := aggregate(ctx, coll, pipeline, sampleRows)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	return readSchema(ctx, cur)
}

func Query(ctx context.Context, coll *mongo.Collection, pipeline json.RawMessage, fields []string) (Result, error) {
	return query(ctx, coll, pipeline, fields, maxRows+1)
}

// Preview returns at most 50 rows, inferring columns when the editor omits them.
func Preview(ctx context.Context, coll *mongo.Collection, pipeline json.RawMessage, fields []string) (Result, error) {
	if len(fields) == 0 {
		schema, err := Schema(ctx, coll, pipeline)
		if err != nil {
			return Result{}, err
		}
		for _, f := range schema {
			fields = append(fields, f.Name)
		}
		if len(fields) == 0 {
			return Result{Fields: []string{}, Rows: [][]any{}}, nil
		}
	}
	return query(ctx, coll, pipeline, fields, 50)
}
func query(ctx context.Context, coll *mongo.Collection, pipeline json.RawMessage, fields []string, limit int64) (Result, error) {
	if len(fields) == 0 || len(fields) > maxFields {
		return Result{}, fmt.Errorf("%w: select 1 to %d fields", ErrInvalid, maxFields)
	}
	seen := make(map[string]bool, len(fields))
	for _, name := range fields {
		if name == "" || len(name) > maxNameBytes || seen[name] {
			return Result{}, fmt.Errorf("%w: invalid or duplicate requested field", ErrInvalid)
		}
		seen[name] = true
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	cur, err := aggregate(ctx, coll, pipeline, limit)
	if err != nil {
		return Result{}, err
	}
	defer cur.Close(ctx)
	return readQuery(ctx, cur, fields)
}

func aggregate(ctx context.Context, coll *mongo.Collection, raw json.RawMessage, limit int64) (*mongo.Cursor, error) {
	p, err := buildPipeline(raw, limit)
	if err != nil {
		return nil, err
	}
	return coll.Aggregate(ctx, p, options.Aggregate().SetAllowDiskUse(false).SetBatchSize(32))
}

func readSchema(ctx context.Context, c *mongo.Cursor) ([]Field, error) {
	types := map[string]string{}
	err := scan(ctx, c, sampleRows, func(_ int, doc map[string]bson.RawValue) error {
		for name, v := range doc {
			kind, _ := cell(v)
			types[name] = mergeType(types[name], kind)
		}
		if len(types) > maxFields {
			return fmt.Errorf("%w: too many schema fields", ErrLimit)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	fields := make([]Field, 0, len(types))
	for _, name := range slices.Sorted(maps.Keys(types)) {
		fields = append(fields, Field{Name: name, Type: displayType(types[name])})
	}
	return fields, nil
}

func readQuery(ctx context.Context, c *mongo.Cursor, fields []string) (Result, error) {
	result := Result{Fields: fields, Rows: [][]any{}}
	header, err := json.Marshal(result)
	if err != nil {
		return Result{}, err
	}
	size := len(header)
	types := make([]string, len(fields))
	err = scan(ctx, c, maxRows, func(index int, doc map[string]bson.RawValue) error {
		row := make([]any, len(fields))
		for i, name := range fields {
			kind, value := cell(doc[name])
			if index < sampleRows {
				types[i] = mergeType(types[i], kind)
			} else if expected := displayType(types[i]); kind != "" && expected != "TEXT" && kind != expected {
				return fmt.Errorf("%w: field type changed after schema sample", ErrInvalid)
			}
			row[i] = value
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return err
		}
		size += len(encoded)
		if index > 0 {
			size++
		}
		if size > maxResultBytes {
			return fmt.Errorf("%w: response exceeds 2 MiB", ErrLimit)
		}
		result.Rows = append(result.Rows, row)
		return nil
	})
	if err != nil {
		return Result{}, err
	}

	// Resolve mixed and null-only sample columns to TEXT across the complete result.
	for _, row := range result.Rows {
		for i, v := range row {
			if v != nil && displayType(types[i]) == "TEXT" {
				if _, ok := v.(string); !ok {
					row[i] = fmt.Sprint(v)
				}
			}
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return Result{}, err
	}
	if len(encoded) > maxResultBytes {
		return Result{}, fmt.Errorf("%w: response exceeds 2 MiB", ErrLimit)
	}
	return result, ctx.Err()
}

func mergeType(old, kind string) string {
	if old == "" {
		return kind
	}
	if kind != "" && kind != old {
		return "TEXT"
	}
	return old
}
func displayType(kind string) string {
	if kind == "" {
		return "TEXT"
	}
	return kind
}

func scan(ctx context.Context, c *mongo.Cursor, limit int, visit func(int, map[string]bson.RawValue) error) error {
	for i := 0; c.Next(ctx); i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i >= limit {
			return fmt.Errorf("%w: too many rows", ErrLimit)
		}
		if len(c.Current) > 1<<20 {
			return fmt.Errorf("%w: a result document exceeds 1 MiB", ErrLimit)
		}
		doc := map[string]bson.RawValue{}
		if err := flatten(c.Current, "", 0, doc); err != nil {
			return err
		}
		if err := visit(i, doc); err != nil {
			return err
		}
	}
	if err := c.Err(); err != nil {
		return err
	}
	return ctx.Err()
}

var escapeKey = strings.NewReplacer(`\`, `\\`, `.`, `\.`)

func flatten(doc bson.Raw, prefix string, depth int, out map[string]bson.RawValue) error {
	if depth > 32 {
		return fmt.Errorf("%w: document nesting exceeds 32", ErrLimit)
	}
	elems, err := doc.Elements()
	if err != nil {
		return err
	}
	for _, e := range elems {
		key := escapeKey.Replace(e.Key())
		if key == "" {
			key = `\0`
		}
		name := prefix + key
		if len(name) > maxNameBytes {
			return fmt.Errorf("%w: field name exceeds %d bytes", ErrLimit, maxNameBytes)
		}
		v := e.Value()
		if v.Type == bson.TypeEmbeddedDocument && len(v.Value) > 5 {
			if err := flatten(v.Document(), name+".", depth+1, out); err != nil {
				return err
			}
			continue
		}
		if _, exists := out[name]; exists {
			return fmt.Errorf("%w: duplicate BSON field", ErrInvalid)
		}
		out[name] = v
		if len(out) > maxFields {
			return fmt.Errorf("%w: too many document fields", ErrLimit)
		}
	}
	return nil
}

func cell(v bson.RawValue) (string, any) {
	switch v.Type {
	case 0, bson.TypeNull, bson.TypeUndefined:
		return "", nil
	case bson.TypeString:
		return "TEXT", v.StringValue()
	case bson.TypeBoolean:
		return "BOOLEAN", v.Boolean()
	case bson.TypeInt32:
		return "NUMBER", int64(v.Int32())
	case bson.TypeInt64:
		n := v.Int64()
		const safe = 1<<53 - 1
		if n > safe || n < -safe {
			return "TEXT", strconv.FormatInt(n, 10)
		}
		return "NUMBER", n
	case bson.TypeDouble:
		n := v.Double()
		if math.IsInf(n, 0) || math.IsNaN(n) {
			return "TEXT", strconv.FormatFloat(n, 'g', -1, 64)
		}
		return "NUMBER", n
	case bson.TypeObjectID:
		return "TEXT", v.ObjectID().Hex()
	case bson.TypeDecimal128:
		return "TEXT", v.Decimal128().String()
	case bson.TypeDateTime:
		d := time.UnixMilli(v.DateTime()).UTC()
		if d.Year() >= 1 && d.Year() <= 9999 {
			return "YEAR_MONTH_DAY_SECOND", d.Format("20060102150405")
		}
	}
	return "TEXT", v.String()
}
