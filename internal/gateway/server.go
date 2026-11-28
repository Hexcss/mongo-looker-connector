package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/Hexcss/mongo-looker-connector/internal/control"
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
	r = r.WithContext(ctx)
	if s.admin != nil && r.URL.Path != "/health" && r.URL.Path != "/version" {
		var ok bool
		r, ok = s.authorize(w, r)
		if !ok {
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes(version, revision string) {
	s.mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	s.mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, map[string]string{"version": version, "revision": revision})
	})
	s.mux.HandleFunc("GET /api/v1/connections", func(w http.ResponseWriter, r *http.Request) {
		ids, err := s.visibleIDs(r)
		if err != nil {
			controlError(w, err)
			return
		}
		reply(w, http.StatusOK, map[string][]string{"connections": ids})
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
	if r.PathValue("id") == control.DatasetConnection {
		s.datasetNamespaces(w, r, true)
		return
	}
	c, closeClient, err := s.resolve(r, r.PathValue("id"), "", "", nil)
	if err != nil {
		controlError(w, err)
		return
	}
	defer closeClient()
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
		!reportConnectionID(params.Get("connectionId")) || !validName(params.Get("database")) {
		invalidRequest(w)
		return
	}
	db := params.Get("database")
	if params.Get("connectionId") == control.DatasetConnection {
		if db != control.DatasetDatabase {
			notConfigured(w)
			return
		}
		s.datasetNamespaces(w, r, false)
		return
	}
	c, closeClient, err := s.resolve(r, params.Get("connectionId"), db, "", nil)
	if err != nil {
		controlError(w, err)
		return
	}
	defer closeClient()
	allowed := c.databases[db]
	if allowed == nil {
		notConfigured(w)
		return
	}
	parent, cancel := c.operationContext(r.Context())
	defer cancel()
	ctx := discoveryContext{parent}
	database := c.client.Database(db)
	opts := options.ListCollections().SetNameOnly(true)
	cursor, err := database.ListCollections(ctx, bson.D{}, opts)
	var commandErr mongo.CommandError
	if errors.As(err, &commandErr) && commandErr.Code == 13 {
		// MongoDB permits find-only identities to list their own collections
		// with this flag. Firestore accepts the initial name-only command.
		cursor, err = database.ListCollections(ctx, bson.D{}, opts.SetAuthorizedCollections(true))
	}
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
	dateRange  *engine.DateRange
	filters    map[string]json.RawMessage
}

// Decode the envelope separately from the pipeline. Each key must appear
// exactly once at most, with its documented case and a non-null value.
func parseReport(body []byte) (reportRequest, error) {
	var req reportRequest
	var rawDates json.RawMessage
	fields := map[string]any{
		"connectionId": &req.connection, "database": &req.database, "collection": &req.collection,
		"pipeline": &req.pipeline, "fields": &req.fields, "dateRange": &rawDates, "filters": &req.filters,
	}
	if err := decodeReportObject(body, fields); err != nil {
		return req, err
	}
	if rawDates != nil {
		dates := new(engine.DateRange)
		keys := map[string]any{"field": &dates.Field, "startDate": &dates.StartDate, "endDate": &dates.EndDate}
		if err := decodeReportObject(rawDates, keys); err != nil || len(keys) != 0 {
			return req, engine.ErrInvalid
		}
		req.dateRange = dates
	}
	if (fields["filters"] == nil && (req.filters == nil || req.connection != control.DatasetConnection)) || !reportConnectionID(req.connection) || !validName(req.database) || !validName(req.collection) ||
		(fields["fields"] == nil && req.fields == nil) || (len(req.pipeline) > 0 && req.pipeline[0] != '[') {
		return req, engine.ErrInvalid
	}
	return req, nil
}

func decodeReportObject(body []byte, fields map[string]any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	if open, err := dec.Token(); err != nil || open != json.Delim('{') || !utf8.Valid(body) {
		return engine.ErrInvalid
	}
	for dec.More() {
		rawKey, err := dec.Token()
		key, ok := rawKey.(string)
		dest := fields[key]
		if err != nil || !ok || dest == nil || dec.Decode(dest) != nil {
			return engine.ErrInvalid
		}
		delete(fields, key)
	}
	if closeToken, err := dec.Token(); err != nil || closeToken != json.Delim('}') || dec.Decode(new(any)) != io.EOF {
		return engine.ErrInvalid
	}
	return nil
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
	if err != nil || (req.dateRange != nil && r.Pattern != "POST /api/v1/query" && !(r.Pattern == "POST /api/v1/admin/preview" && req.connection == control.DatasetConnection)) {
		invalidRequest(w)
		return
	}
	if req.connection == control.DatasetConnection {
		s.datasetReport(w, r, req)
		return
	}
	c, closeClient, err := s.resolve(r, req.connection, req.database, req.collection, req.pipeline)
	if err != nil {
		controlError(w, err)
		return
	}
	defer closeClient()
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
	var result engine.Result
	if r.Pattern == "POST /api/v1/admin/preview" {
		result, err = engine.Preview(ctx, coll, req.pipeline, req.fields)
	} else {
		result, err = engine.Query(ctx, coll, req.pipeline, req.fields, req.dateRange)
	}
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
