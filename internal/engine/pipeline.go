// Package engine executes bounded read-only reports against configured MongoDB collections.
package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

var (
	ErrInvalid = errors.New("invalid query")
	ErrLimit   = errors.New("query limit exceeded")
)

var operators = strings.Fields(`$and $or $nor $not $eq $ne $lt $lte $gt $gte $in $nin $exists $elemMatch $expr
	$sum $avg $min $max $first $last $add $subtract $multiply $divide $mod $abs $round
	$concat $toLower $toUpper $ifNull $cond $year $month $dayOfMonth $dateToString $size $arrayElemAt`)

func buildPipeline(raw json.RawMessage, limit int64) (mongo.Pipeline, error) {
	stages, err := parsePipeline(raw)
	if err != nil {
		return nil, err
	}
	return append(stages, bson.D{{Key: "$limit", Value: limit}}), nil
}

func parsePipeline(raw json.RawMessage) (mongo.Pipeline, error) {
	if len(raw) > 32<<10 {
		return nil, fmt.Errorf("%w: pipeline exceeds 32 KiB", ErrLimit)
	}
	raw = bytes.TrimSpace(raw)
	var items []json.RawMessage
	if len(raw) > 0 && (!utf8.Valid(raw) || raw[0] != '[' || json.Unmarshal(raw, &items) != nil) {
		return nil, fmt.Errorf("%w: pipeline must be a JSON array", ErrInvalid)
	}
	if len(items) > 32 {
		return nil, fmt.Errorf("%w: maximum 32 pipeline stages", ErrLimit)
	}
	pipeline := make(mongo.Pipeline, 0, len(items)+1)
	for _, item := range items {
		dec := json.NewDecoder(bytes.NewReader(item))
		dec.UseNumber()
		v, err := parseValue(dec, 0)
		if err != nil {
			return nil, err
		}
		stage, ok := v.(bson.D)
		if !ok || len(stage) != 1 || !validStage(stage[0]) {
			return nil, fmt.Errorf("%w: invalid stage name or arguments", ErrInvalid)
		}
		pipeline = append(pipeline, stage)
	}
	return pipeline, nil
}

// LookupCollections returns every foreign collection named by a validated pipeline.
// All supported joins use literal collection names in the database of the source.
func LookupCollections(raw json.RawMessage) ([]string, error) {
	stages, err := parsePipeline(raw)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, stage := range stages {
		if stage[0].Key == "$lookup" {
			for _, arg := range stage[0].Value.(bson.D) {
				if arg.Key == "from" {
					names = append(names, arg.Value.(string))
				}
			}
		}
	}
	return names, nil
}

func validLookup(doc bson.D) bool {
	// parseValue already rejects duplicate keys. Exactly these four strings
	// describe an equality join; subpipelines and foreign databases are excluded.
	if len(doc) != 4 {
		return false
	}
	for _, arg := range doc {
		value, ok := arg.Value.(string)
		if !ok || value == "" {
			return false
		}
		switch arg.Key {
		case "from":
			if strings.TrimSpace(value) != value || strings.ContainsAny(value, "$\x00") {
				return false
			}
		case "localField", "foreignField", "as":
			if !validDatasetPath(value) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func validStage(stage bson.E) bool {
	doc, isDoc := stage.Value.(bson.D)
	switch stage.Key {
	case "$lookup":
		return validLookup(doc)
	case "$match", "$addFields", "$set":
		return isDoc
	case "$project":
		return isDoc && len(doc) > 0
	case "$group":
		return slices.ContainsFunc(doc, func(e bson.E) bool { return e.Key == "_id" })
	case "$sort":
		for _, e := range doc {
			direction, ok := e.Value.(int64)
			if !ok || (direction != -1 && direction != 1) {
				return false
			}
		}
		return len(doc) > 0
	case "$skip", "$limit":
		n, ok := stage.Value.(int64)
		return ok && (n > 0 || (n == 0 && stage.Key == "$skip"))
	case "$unwind":
		path, ok := stage.Value.(string)
		return ok && len(path) > 1 && path[0] == '$'
	case "$count":
		name, ok := stage.Value.(string)
		return ok && name != "" && name[0] != '$' && !strings.ContainsAny(name, ".\x00")
	case "$unset":
		validName := func(v any) bool {
			s, ok := v.(string)
			return ok && s != "" && s[0] != '$' && !strings.ContainsRune(s, 0)
		}
		if arr, ok := stage.Value.(bson.A); ok {
			return len(arr) > 0 && !slices.ContainsFunc(arr, func(v any) bool { return !validName(v) })
		}
		return validName(stage.Value)
	}
	return false
}

// JSON tokens preserve MongoDB's sort-key ordering and expose duplicate names.
func parseValue(dec *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, fmt.Errorf("%w: pipeline nesting exceeds 32", ErrLimit)
	}
	token, err := dec.Token()
	if err != nil {
		return nil, ErrInvalid
	}
	switch v := token.(type) {
	case json.Delim:
		doc := bson.D{}
		arr := bson.A{}
		seen := make(map[string]bool)
		for dec.More() {
			var key string
			if v == '{' {
				k, err := dec.Token()
				if err != nil {
					return nil, ErrInvalid
				}
				key = k.(string)
				if seen[key] || key == "" || strings.ContainsRune(key, 0) {
					return nil, fmt.Errorf("%w: invalid or duplicate key", ErrInvalid)
				}
				if depth > 0 && strings.HasPrefix(key, "$") && !slices.Contains(operators, key) {
					return nil, fmt.Errorf("%w: unsupported operator", ErrInvalid)
				}
				seen[key] = true
			}
			child, err := parseValue(dec, depth+1)
			if err != nil {
				return nil, err
			}
			if v == '{' {
				doc = append(doc, bson.E{Key: key, Value: child})
			} else {
				arr = append(arr, child)
			}
		}
		if _, err := dec.Token(); err != nil {
			return nil, ErrInvalid
		}
		if v == '{' {
			return doc, nil
		}
		return arr, nil
	case json.Number:
		if strings.ContainsAny(v.String(), ".eE") {
			if n, err := v.Float64(); err == nil {
				return n, nil
			}
		} else if n, err := v.Int64(); err == nil {
			return n, nil
		}
		return nil, fmt.Errorf("%w: number out of range", ErrInvalid)
	case string:
		if strings.HasPrefix(v, "$$") {
			return nil, fmt.Errorf("%w: variables are unsupported", ErrInvalid)
		}
	}
	return token, nil
}
