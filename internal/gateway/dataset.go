package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/Hexcss/mongo-looker-connector/internal/control"
	"github.com/Hexcss/mongo-looker-connector/internal/engine"
)

func reportConnectionID(id string) bool {
	return connectionID.MatchString(id) || id == control.DatasetConnection
}

// Only administrators receive the publisher's pipeline and physical namespace.
type datasetInfo struct {
	control.Dataset
	PipelineText string `json:"pipelineText"`
}

func describeDataset(d control.Dataset) datasetInfo {
	return datasetInfo{d, string(d.Pipeline)}
}

func (s *Server) adminDatasets(w http.ResponseWriter, r *http.Request) {
	records, err := s.admin.store.Datasets(r.Context())
	if err != nil {
		controlError(w, err)
		return
	}
	items := make([]datasetInfo, 0, len(records))
	for _, d := range records {
		items = append(items, describeDataset(d))
	}
	reply(w, 200, map[string]any{"datasets": items})
}

func (s *Server) saveDataset(w http.ResponseWriter, r *http.Request) {
	var d control.Dataset
	if !adminBody(w, r, &d) {
		return
	}
	create := r.Method == "POST"
	if !create {
		id := r.PathValue("id")
		if d.ID != "" && d.ID != id {
			invalidRequest(w)
			return
		}
		d.ID = id
	}
	if !connectionID.MatchString(d.ID) || !connectionID.MatchString(d.ConnectionID) || !validName(d.Database) ||
		!validName(d.Collection) || create != (d.Revision == "") || len(d.Pipeline) == 0 || d.Pipeline[0] != '[' {
		invalidRequest(w)
		return
	}
	if err := engine.ValidateDataset(d.Pipeline, d.Fields); err != nil {
		operationError(w, r.Context(), err)
		return
	}
	source, err := s.record(r.Context(), d.ConnectionID)
	if err != nil {
		controlError(w, err)
		return
	}
	if !slices.Contains(source.Databases[d.Database], d.Collection) {
		notConfigured(w)
		return
	}
	d.SourceGeneration = source.Generation
	record, err := s.admin.store.SaveDataset(r.Context(), d)
	if err != nil {
		controlError(w, err)
		return
	}
	status := http.StatusOK
	if create {
		status = http.StatusCreated
	}
	reply(w, status, describeDataset(record))
}

func (s *Server) deleteDataset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision string `json:"revision"`
	}
	if !adminBody(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if !connectionID.MatchString(id) || in.Revision == "" {
		invalidRequest(w)
		return
	}
	if err := s.admin.store.DeleteDataset(r.Context(), id, in.Revision); err != nil {
		controlError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) datasetSource(ctx context.Context, d control.Dataset) (control.Connection, error) {
	c, err := s.record(ctx, d.ConnectionID)
	if err != nil {
		return c, err
	}
	if c.Generation != d.SourceGeneration || !slices.Contains(c.Databases[d.Database], d.Collection) {
		return control.Connection{}, control.ErrNotFound
	}
	return c, nil
}

func (s *Server) datasetGrant(ctx context.Context, scope scope) (control.Grant, error) {
	if !connectionID.MatchString(scope.DatasetID) || scope.ConnectionID != "" || scope.Database != "" || scope.Collection != "" || len(scope.Fields) == 0 {
		return control.Grant{}, control.ErrInvalid
	}
	d, err := s.admin.store.GetDataset(ctx, scope.DatasetID)
	if err != nil {
		return control.Grant{}, err
	}
	if _, err = s.datasetSource(ctx, d); err != nil {
		return control.Grant{}, err
	}
	if err = engine.ValidateDatasetQuery(d.Fields, scope.Fields, nil, nil); err != nil {
		return control.Grant{}, control.ErrInvalid
	}
	return control.Grant{ConnectionID: control.DatasetConnection, Database: control.DatasetDatabase, Collection: d.ID,
		Generation: d.Revision, Fields: scope.Fields}, nil
}

type datasetView struct {
	dataset control.Dataset
	source  control.Connection
	fields  []engine.Field
}

func (s *Server) datasetView(ctx context.Context, id string, t *control.Token) (datasetView, error) {
	var view datasetView
	if s.admin == nil || !connectionID.MatchString(id) {
		return view, control.ErrNotFound
	}
	var grant control.Grant
	if t != nil {
		idx := slices.IndexFunc(t.Grants, func(g control.Grant) bool {
			return g.ConnectionID == control.DatasetConnection && g.Database == control.DatasetDatabase && g.Collection == id
		})
		if idx < 0 {
			return view, control.ErrNotFound
		}
		grant = t.Grants[idx]
	}
	d, err := s.admin.store.GetDataset(ctx, id)
	if err != nil {
		return view, err
	}
	fields := d.Fields
	if t != nil {
		if grant.Generation != d.Revision || len(grant.Fields) == 0 {
			return view, control.ErrNotFound
		}
		fields = make([]engine.Field, 0, len(grant.Fields))
		for _, f := range d.Fields {
			if slices.Contains(grant.Fields, f.Name) {
				fields = append(fields, f)
			}
		}
		if len(fields) != len(grant.Fields) {
			return view, control.ErrNotFound
		}
	}
	source, err := s.datasetSource(ctx, d)
	if err != nil {
		return view, err
	}
	return datasetView{d, source, fields}, nil
}

func (s *Server) visibleDatasets(r *http.Request) ([]string, error) {
	names := []string{}
	if s.admin == nil {
		return names, nil
	}
	ctx := r.Context()
	if t := reportToken(r); t != nil {
		for _, g := range t.Grants {
			if g.ConnectionID != control.DatasetConnection {
				continue
			}
			if _, err := s.datasetView(ctx, g.Collection, t); err != nil {
				if errors.Is(err, control.ErrNotFound) {
					continue
				}
				return nil, err
			}
			names = append(names, g.Collection)
		}
	} else {
		records, err := s.admin.store.Datasets(ctx)
		if err != nil {
			return nil, err
		}
		for _, d := range records {
			if _, err := s.datasetSource(ctx, d); err != nil {
				if errors.Is(err, control.ErrNotFound) {
					continue
				}
				return nil, err
			}
			names = append(names, d.ID)
		}
	}
	slices.Sort(names)
	return names, nil
}

func (s *Server) datasetNamespaces(w http.ResponseWriter, r *http.Request, databases bool) {
	names, err := s.visibleDatasets(r)
	if err != nil {
		controlError(w, err)
		return
	}
	if len(names) == 0 {
		notConfigured(w)
		return
	}
	if databases {
		reply(w, 200, map[string][]string{"databases": {control.DatasetDatabase}})
		return
	}
	reply(w, 200, map[string][]string{"collections": names})
}

// The connector appends an _id sort for deterministic ordering across field batches.
// A reporting client can otherwise only run the administrator's published pipeline.
func datasetSort(pipeline json.RawMessage) (bool, error) {
	if len(pipeline) == 0 {
		return false, nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, pipeline); err != nil {
		return false, engine.ErrInvalid
	}
	switch compact.String() {
	case "[]":
		return false, nil
	case `[{"$sort":{"_id":1}}]`:
		return true, nil
	default:
		return false, engine.ErrInvalid
	}
}

func (s *Server) datasetReport(w http.ResponseWriter, r *http.Request, req reportRequest) {
	if req.database != control.DatasetDatabase {
		notConfigured(w)
		return
	}
	token := reportToken(r)
	view, err := s.datasetView(r.Context(), req.collection, token)
	if err != nil {
		controlError(w, err)
		return
	}
	sortByID, err := datasetSort(req.pipeline)
	if err != nil {
		invalidRequest(w)
		return
	}
	preview := r.Pattern == "POST /api/v1/admin/preview"
	schema := r.Pattern == "POST /api/v1/schema"
	if (schema || preview) && len(req.fields) == 0 {
		for _, f := range view.fields {
			req.fields = append(req.fields, f.Name)
		}
	}
	if err := engine.ValidateDatasetQuery(view.fields, req.fields, req.dateRange, req.filters); err != nil {
		operationError(w, r.Context(), err)
		return
	}
	if schema {
		if req.filters != nil {
			invalidRequest(w)
			return
		}
		reply(w, 200, map[string][]engine.Field{"fields": view.fields})
		return
	}
	var key [32]byte
	if !preview {
		// All authorization, field validation and current source checks above run
		// on both cache hits and misses. Separate tokens never share a result.
		principal := "admin"
		if token != nil {
			principal = "token:" + token.ID
		}
		keyData, err := json.Marshal([]any{principal, view.dataset.ID, view.dataset.Revision, view.source.Generation, view.source.Revision,
			view.fields, req.fields, req.dateRange, req.filters, sortByID})
		if err != nil {
			invalidRequest(w)
			return
		}
		key = sha256.Sum256(keyData)
		if data := s.admin.cache.get(key); data != nil {
			writeDatasetResult(w, data, "HIT")
			return
		}
	}
	source, closeClient, err := s.openRecord(r.Context(), view.source, view.source.Databases)
	if err != nil {
		controlError(w, err)
		return
	}
	defer closeClient()
	ctx, cancel := source.operationContext(r.Context())
	defer cancel()
	d := view.dataset
	result, err := engine.DatasetQuery(ctx, source.client.Database(d.Database).Collection(d.Collection), d.Pipeline,
		view.fields, req.fields, req.dateRange, req.filters, sortByID, preview)
	if err != nil {
		operationError(w, ctx, err)
		return
	}
	if preview {
		reply(w, 200, result)
		return
	}
	data, err := json.Marshal(result)
	if err != nil {
		fail(w, 500, "encoding_error", "Unable to encode the response.")
		return
	}
	s.admin.cache.put(key, data)
	writeDatasetResult(w, data, "MISS")
}

func writeDatasetResult(w http.ResponseWriter, data []byte, status string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-MLC-Cache", status)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
