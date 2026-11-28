package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"

	"github.com/Hexcss/mongo-looker-connector/internal/control"
	"github.com/Hexcss/mongo-looker-connector/internal/engine"
)

var connectorDeployment = regexp.MustCompile(`^[A-Za-z0-9_-]{8,256}$`)
var gatewayOrigin = regexp.MustCompile(`^https://([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]*[a-z0-9])?(:[1-9][0-9]{0,4})?$`)

func (s *Server) enableWorkspace() {
	deployment, origin := os.Getenv("MLC_LOOKER_CONNECTOR_ID"), os.Getenv("MLC_LOOKER_GATEWAY_ORIGIN")
	link := func(w http.ResponseWriter, id string) string {
		if !connectorDeployment.MatchString(deployment) || !gatewayOrigin.MatchString(origin) {
			fail(w, 503, "looker_unconfigured", "Set the Looker Studio connector deployment ID and public gateway origin for this installation.")
			return ""
		}
		// SELECT_SINGLE stores the encoded option value emitted by Code.gs.
		config, _ := json.Marshal(map[string]string{"gatewayOrigin": url.QueryEscape(origin), "connectionId": "%40datasets", "collection": id})
		q := url.Values{"connectorId": {deployment}, "connectorConfig": {string(config)}}
		return "https://lookerstudio.google.com/datasources/create?" + q.Encode()
	}
	s.mux.HandleFunc("GET /api/v1/admin/workspace/looker/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !connectionID.MatchString(id) {
			invalidRequest(w)
			return
		}
		if _, err := s.datasetView(r.Context(), id, nil); err != nil {
			controlError(w, err)
			return
		}
		if target := link(w, id); target != "" {
			reply(w, 200, map[string]string{"datasetId": id, "url": target})
		}
	})
	run := func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID           string          `json:"id"`
			ConnectionID string          `json:"connectionId"`
			Database     string          `json:"database"`
			Collection   string          `json:"collection"`
			Pipeline     json.RawMessage `json:"pipeline"`
		}
		if !adminBody(w, r, &in) {
			return
		}
		publish := r.Pattern == "POST /api/v1/admin/workspace/open"
		if !connectionID.MatchString(in.ConnectionID) || !validName(in.Database) || !validName(in.Collection) ||
			len(in.Pipeline) == 0 || in.Pipeline[0] != '[' || (publish && !connectionID.MatchString(in.ID)) || (!publish && in.ID != "") {
			invalidRequest(w)
			return
		}
		var target string
		if publish {
			target = link(w, in.ID)
			if target == "" {
				return
			}
		}
		source, err := s.record(r.Context(), in.ConnectionID)
		if err != nil {
			controlError(w, err)
			return
		}
		if err := authorizePipeline(source.Databases[in.Database], in.Collection, in.Pipeline); err != nil {
			controlError(w, err)
			return
		}
		db, closeClient, err := s.openRecord(r.Context(), source, source.Databases)
		if err != nil {
			controlError(w, err)
			return
		}
		defer closeClient()
		ctx, cancel := db.operationContext(r.Context())
		defer cancel()
		coll := db.client.Database(in.Database).Collection(in.Collection)
		if r.Pattern == "POST /api/v1/admin/workspace/explain" {
			stats, err := engine.Explain(ctx, coll, in.Pipeline, db.readPreference)
			if err != nil {
				operationError(w, ctx, err)
				return
			}
			reply(w, http.StatusOK, stats)
			return
		}
		fields, err := engine.Schema(ctx, coll, in.Pipeline)
		if err != nil {
			operationError(w, ctx, err)
			return
		}
		names := make([]string, len(fields))
		for i, f := range fields {
			names[i] = f.Name
		}
		if publish {
			if len(fields) == 0 {
				fail(w, 400, "empty_schema", "The pipeline returned no fields. Preview a pipeline with at least one result before saving.")
				return
			}
			if err := engine.ValidateDataset(in.Pipeline, fields); err != nil {
				operationError(w, ctx, err)
				return
			}
			if _, err := engine.DatasetQuery(ctx, coll, in.Pipeline, fields, names, nil, nil, false, true); err != nil {
				operationError(w, ctx, err)
				return
			}
			definition := control.Dataset{ID: in.ID, ConnectionID: source.ID, Database: in.Database, Collection: in.Collection, SourceGeneration: source.Generation, Pipeline: in.Pipeline, Fields: fields}
			status := http.StatusCreated
			_, err = s.admin.store.SaveDataset(ctx, definition)
			if errors.Is(err, control.ErrConflict) {
				existing, lookupErr := s.admin.store.GetDataset(ctx, in.ID)
				if lookupErr != nil {
					controlError(w, lookupErr)
					return
				}
				if sameWorkspaceDataset(existing, definition) {
					status, err = http.StatusOK, nil
				} else {
					fail(w, 409, "dataset_exists", "This ID belongs to a different dataset. Choose another ID or reopen it from Datasets.")
					return
				}
			}
			if err != nil {
				controlError(w, err)
				return
			}
			reply(w, status, map[string]string{"datasetId": in.ID, "url": target})
			return
		}
		rows := [][]any{}
		if len(fields) > 0 {
			result, err := engine.Preview(ctx, coll, in.Pipeline, names)
			if err != nil {
				operationError(w, ctx, err)
				return
			}
			rows = result.Rows
		}
		reply(w, 200, map[string]any{"fields": fields, "rows": rows})
	}
	s.mux.HandleFunc("POST /api/v1/admin/workspace/explain", run)
	s.mux.HandleFunc("POST /api/v1/admin/workspace/preview", run)
	s.mux.HandleFunc("POST /api/v1/admin/workspace/open", run)
}

// JSON object key order matters in MongoDB expressions such as compound sorts.
func sameWorkspaceDataset(a, b control.Dataset) bool {
	var left, right bytes.Buffer
	return a.ConnectionID == b.ConnectionID && a.Database == b.Database && a.Collection == b.Collection &&
		a.SourceGeneration == b.SourceGeneration && slices.Equal(a.Fields, b.Fields) &&
		json.Compact(&left, a.Pipeline) == nil && json.Compact(&right, b.Pipeline) == nil && bytes.Equal(left.Bytes(), right.Bytes())
}
