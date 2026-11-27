package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/Hexcss/mongo-looker-connector/internal/engine"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const requestLimit = 256 << 10

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.RawQuery != "" && r.URL.Path != "/api/v1/collections" {
		invalidRequest(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	s.mux.ServeHTTP(w, r.WithContext(ctx))
}

func (s *Server) routes(version, revision string) {
	s.mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	s.mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, map[string]string{"version": version, "revision": revision})
	})
	s.mux.HandleFunc("GET /api/v1/connections", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, map[string][]string{"connections": slices.Sorted(maps.Keys(s.connections))})
	})
	s.mux.HandleFunc("GET /api/v1/connections/{id}/databases", s.listDatabases)
	s.mux.HandleFunc("GET /api/v1/collections", s.listCollections)
	s.mux.HandleFunc("POST /api/v1/schema", s.report)
	s.mux.HandleFunc("POST /api/v1/query", s.report)
}

func (c connection) operationContext(parent context.Context) (context.Context, context.CancelFunc) {
	if c.timeout > 0 {
		return context.WithTimeout(parent, c.timeout)
	}
	return context.WithCancel(parent)
}

// The Go driver turns a visible deadline into maxTimeMS. Firestore rejects
// that option on discovery commands. Done and Err still enforce the deadline
// and propagate client disconnects to in-flight driver I/O.
type discoveryContext struct{ context.Context }

func (discoveryContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (s *Server) listDatabases(w http.ResponseWriter, r *http.Request) {
	c, ok := s.connections[r.PathValue("id")]
	if !ok {
		notConfigured(w)
		return
	}
	ctx, cancel := c.operationContext(r.Context())
	defer cancel()
	names, err := c.client.ListDatabaseNames(discoveryContext{ctx}, bson.D{})
	if err != nil {
		operationError(w, ctx, err)
		return
	}
	names = slices.DeleteFunc(names, func(name string) bool { return c.databases[name] == nil })
	slices.Sort(names)
	reply(w, http.StatusOK, map[string][]string{"databases": names})
}

func (s *Server) listCollections(w http.ResponseWriter, r *http.Request) {
	params, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(params) != 2 || len(params["connectionId"]) != 1 || len(params["database"]) != 1 ||
		!connectionID.MatchString(params.Get("connectionId")) || !validName(params.Get("database")) {
		invalidRequest(w)
		return
	}
	c := s.connections[params.Get("connectionId")]
	db := params.Get("database")
	allowed := c.databases[db]
	if allowed == nil {
		notConfigured(w)
		return
	}
	parent, cancel := c.operationContext(r.Context())
	defer cancel()
	ctx := discoveryContext{parent}
	cursor, err := c.client.Database(db).ListCollections(ctx, bson.D{}, options.ListCollections().SetNameOnly(true))
	if err != nil {
		operationError(w, ctx, err)
		return
	}
	defer cursor.Close(ctx)
	names := []string{}
	for cursor.Next(ctx) {
		var item struct{ Name string }
		if err := cursor.Decode(&item); err != nil {
			operationError(w, ctx, err)
			return
		}
		if _, ok := slices.BinarySearch(allowed, item.Name); ok {
			names = append(names, item.Name)
		}
	}
	if err := cursor.Err(); err != nil {
		operationError(w, ctx, err)
		return
	}
	slices.Sort(names)
	reply(w, http.StatusOK, map[string][]string{"collections": names})
}

type reportRequest struct {
	connection string
	database   string
	collection string
	pipeline   json.RawMessage
	fields     []string
}

// Decode the envelope separately from the pipeline. Each key must appear
// exactly once at most, with its documented case and a non-null value.
func parseReport(body []byte) (reportRequest, error) {
	var req reportRequest
	fields := map[string]any{
		"connectionId": &req.connection, "database": &req.database, "collection": &req.collection,
		"pipeline": &req.pipeline, "fields": &req.fields,
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if open, err := dec.Token(); err != nil || open != json.Delim('{') || !utf8.Valid(body) {
		return req, engine.ErrInvalid
	}
	for dec.More() {
		rawKey, err := dec.Token()
		key, ok := rawKey.(string)
		dest := fields[key]
		if err != nil || !ok || dest == nil || dec.Decode(dest) != nil {
			return req, engine.ErrInvalid
		}
		delete(fields, key)
	}
	if closeToken, err := dec.Token(); err != nil || closeToken != json.Delim('}') || dec.Decode(new(any)) != io.EOF {
		return req, engine.ErrInvalid
	}
	if !connectionID.MatchString(req.connection) || !validName(req.database) || !validName(req.collection) ||
		(fields["fields"] == nil && req.fields == nil) || (len(req.pipeline) > 0 && req.pipeline[0] != '[') {
		return req, engine.ErrInvalid
	}
	return req, nil
}

func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		fail(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Use application/json.")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, requestLimit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			fail(w, http.StatusRequestEntityTooLarge, "limit_exceeded", "Request exceeds the size limit.")
		} else {
			invalidRequest(w)
		}
		return
	}
	req, err := parseReport(body)
	if err != nil {
		invalidRequest(w)
		return
	}
	c := s.connections[req.connection]
	if _, ok := slices.BinarySearch(c.databases[req.database], req.collection); !ok {
		notConfigured(w)
		return
	}
	coll := c.client.Database(req.database).Collection(req.collection)
	ctx, cancel := c.operationContext(r.Context())
	defer cancel()
	if r.Pattern == "POST /api/v1/schema" {
		fields, err := engine.Schema(ctx, coll, req.pipeline)
		if err != nil {
			operationError(w, ctx, err)
			return
		}
		reply(w, http.StatusOK, map[string][]engine.Field{"fields": fields})
		return
	}
	result, err := engine.Query(ctx, coll, req.pipeline, req.fields)
	if err != nil {
		operationError(w, ctx, err)
		return
	}
	reply(w, http.StatusOK, result)
}

func reply(w http.ResponseWriter, status int, value any) {
	// Marshal before writing headers so encoding failures cannot send a partial JSON response.
	data, err := json.Marshal(value)
	if err != nil {
		fail(w, http.StatusInternalServerError, "encoding_error", "Unable to encode the response.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func fail(w http.ResponseWriter, status int, code, message string) {
	reply(w, status, map[string]map[string]string{"error": {"code": code, "message": message}})
}

func invalidRequest(w http.ResponseWriter) {
	fail(w, http.StatusBadRequest, "invalid_request", "Invalid request or unsupported pipeline.")
}

func notConfigured(w http.ResponseWriter) {
	fail(w, http.StatusNotFound, "not_found", "Connection or namespace is unavailable.")
}

func operationError(w http.ResponseWriter, ctx context.Context, err error) {
	switch {
	case errors.Is(err, engine.ErrInvalid):
		invalidRequest(w)
	case errors.Is(err, engine.ErrLimit):
		fail(w, http.StatusRequestEntityTooLarge, "limit_exceeded", "Report exceeds a gateway limit.")
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || mongo.IsTimeout(err):
		fail(w, http.StatusGatewayTimeout, "timeout", "Database operation timed out.")
	default:
		fail(w, http.StatusBadGateway, "database_error", "Database operation failed.")
	}
}
