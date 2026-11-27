package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Hexcss/mongo-looker-connector/internal/control"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// OpenAdmin starts the protected console and reporting API, even for a new deployment
// that has yet to create its first MongoDB connection.
func OpenAdmin(raw, version, revision string, store catalog, verify func(context.Context, string) bool, ui http.Handler) (*Server, error) {
	if raw == "" {
		raw = "[]"
	}
	s, err := open(raw, version, revision, true)
	if err != nil {
		return nil, err
	}
	s.EnableAdmin(store, verify, ui)
	return s, nil
}
func (s *Server) EnableAdmin(store catalog, verify func(context.Context, string) bool, ui http.Handler) {
	s.admin = &administration{store: store, verify: verify}
	s.mux.Handle("GET /admin/", http.StripPrefix("/admin", ui))
	s.mux.HandleFunc("GET /api/v1/admin/connections", s.adminConnections)
	s.mux.HandleFunc("POST /api/v1/admin/connections", s.saveConnection)
	s.mux.HandleFunc("PUT /api/v1/admin/connections/{id}", s.saveConnection)
	s.mux.HandleFunc("DELETE /api/v1/admin/connections/{id}", s.deleteConnection)
	s.mux.HandleFunc("POST /api/v1/admin/preview", s.report)
	s.mux.HandleFunc("GET /api/v1/admin/datasets", s.adminDatasets)
	s.mux.HandleFunc("POST /api/v1/admin/datasets", s.saveDataset)
	s.mux.HandleFunc("PUT /api/v1/admin/datasets/{id}", s.saveDataset)
	s.mux.HandleFunc("DELETE /api/v1/admin/datasets/{id}", s.deleteDataset)
	s.mux.HandleFunc("GET /api/v1/admin/tokens", s.adminTokens)
	s.mux.HandleFunc("POST /api/v1/admin/tokens", s.issueToken)
	s.mux.HandleFunc("DELETE /api/v1/admin/tokens/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := store.Revoke(r.Context(), r.PathValue("id")); err != nil {
			controlError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

type connectionInfo struct {
	ID        string              `json:"id"`
	Databases map[string][]string `json:"databases"`
	Revision  string              `json:"revision"`
	Managed   bool                `json:"managed"`
}

func (s *Server) info(c control.Connection) connectionInfo {
	_, bootstrap := s.connections[c.ID]
	return connectionInfo{c.ID, c.Databases, c.Revision, !bootstrap}
}
func (s *Server) adminConnections(w http.ResponseWriter, r *http.Request) {
	records, err := s.records(r.Context())
	if err != nil {
		controlError(w, err)
		return
	}
	items := make([]connectionInfo, 0, len(records))
	for _, c := range records {
		items = append(items, s.info(c))
	}
	reply(w, 200, map[string]any{"connections": items})
}

func adminBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if t, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); t != "application/json" {
		fail(w, 415, "unsupported_media_type", "Use application/json.")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, requestLimit))
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	if err == nil {
		err = dec.Decode(new(any))
		if err == io.EOF {
			return true
		}
	}
	var size *http.MaxBytesError
	if errors.As(err, &size) {
		fail(w, 413, "limit_exceeded", "Request exceeds the size limit.")
	} else {
		invalidRequest(w)
	}
	return false
}

// Only accept network driver settings. Managed URIs cannot select local files,
// weaken TLS, or direct cloud workload credentials to arbitrary hosts.
func validManagedURI(raw string) bool {
	if len(raw) > 8192 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || !(u.Scheme == "mongodb" || u.Scheme == "mongodb+srv") || u.Host == "" || u.Fragment != "" || strings.ContainsAny(u.Host, "/\\@") {
		return false
	}
	params, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return false
	}
	opts := map[string]string{}
	for k, v := range params {
		k = strings.ToLower(k)
		if _, exists := opts[k]; exists || len(v) != 1 {
			return false
		}
		switch k {
		case "tls", "authsource", "authmechanism", "authmechanismproperties", "replicaset", "loadbalanced", "retrywrites", "retryreads", "readpreference", "appname", "directconnection", "connecttimeoutms", "serverselectiontimeoutms", "timeoutms":
			opts[k] = v[0]
		default:
			return false
		}
	}
	if opts["tls"] != "true" && !(u.Scheme == "mongodb+srv" && opts["tls"] == "") {
		return false
	}
	// Validate driver options before replacing a working credential. Parse SRV
	// option values as a seed URI so saving a connection does not require DNS.
	validationURI := raw
	if u.Scheme == "mongodb+srv" {
		if strings.ContainsAny(u.Host, ",:[]") || strings.EqualFold(opts["directconnection"], "true") {
			return false
		}
		validationURI = "mongodb://" + strings.TrimPrefix(raw, "mongodb+srv://")
	}
	if err := options.Client().ApplyURI(validationURI).Validate(); err != nil {
		return false
	}
	switch opts["authmechanism"] {
	case "MONGODB-OIDC":
		return u.Scheme == "mongodb" && u.User == nil && !strings.Contains(u.Host, ",") && strings.HasSuffix(strings.ToLower(u.Hostname()), ".firestore.goog") && u.Port() == "443" && opts["authmechanismproperties"] == "ENVIRONMENT:gcp,TOKEN_RESOURCE:FIRESTORE" && opts["loadbalanced"] == "true" && opts["retrywrites"] == "false"
	case "", "SCRAM-SHA-1", "SCRAM-SHA-256":
		return opts["authmechanismproperties"] == ""
	default:
		return false
	}
}

func (s *Server) saveConnection(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID        string              `json:"id"`
		Databases map[string][]string `json:"databases"`
		URI       string              `json:"uri"`
		Revision  string              `json:"revision"`
	}
	if !adminBody(w, r, &in) {
		return
	}
	create := r.Method == http.MethodPost
	if !create {
		id := r.PathValue("id")
		if in.ID != "" && in.ID != id {
			invalidRequest(w)
			return
		}
		in.ID = id
	}
	if !connectionID.MatchString(in.ID) || (create && (in.URI == "" || in.Revision != "")) || (!create && in.Revision == "") || (in.URI != "" && !validManagedURI(in.URI)) {
		invalidRequest(w)
		return
	}
	cfg := connectionConfig{ID: in.ID, URIEnv: "URI", Databases: in.Databases}
	if cfg.validate() != nil {
		invalidRequest(w)
		return
	}
	if _, exists := s.connections[in.ID]; exists {
		controlError(w, control.ErrConflict)
		return
	}
	c, err := s.admin.store.SaveConnection(r.Context(), in.ID, in.Databases, in.URI, in.Revision)
	if err != nil {
		controlError(w, err)
		return
	}
	status := http.StatusOK
	if create {
		status = http.StatusCreated
	}
	reply(w, status, s.info(c))
}
func (s *Server) deleteConnection(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision string `json:"revision"`
	}
	if !adminBody(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if in.Revision == "" || !connectionID.MatchString(id) {
		invalidRequest(w)
		return
	}
	if _, exists := s.connections[id]; exists {
		controlError(w, control.ErrConflict)
		return
	}
	if err := s.admin.store.DeleteConnection(r.Context(), id, in.Revision); err != nil {
		controlError(w, err)
		return
	}
	w.WriteHeader(204)
}

type scope struct {
	ConnectionID string   `json:"connectionId,omitempty"`
	Database     string   `json:"database,omitempty"`
	Collection   string   `json:"collection,omitempty"`
	DatasetID    string   `json:"datasetId,omitempty"`
	Fields       []string `json:"fields,omitempty"`
}
type tokenInfo struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	Grants    []scope   `json:"grants"`
	CreatedAt time.Time `json:"createdAt"`
	Revoked   bool      `json:"revoked"`
}

func publicToken(t control.Token) tokenInfo {
	grants := make([]scope, 0, len(t.Grants))
	for _, g := range t.Grants {
		if g.ConnectionID == control.DatasetConnection {
			grants = append(grants, scope{DatasetID: g.Collection, Fields: g.Fields})
		} else {
			grants = append(grants, scope{ConnectionID: g.ConnectionID, Database: g.Database, Collection: g.Collection})
		}
	}
	return tokenInfo{t.ID, t.Label, grants, t.CreatedAt, t.Revoked}
}
func (s *Server) adminTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.admin.store.Tokens(r.Context())
	if err != nil {
		controlError(w, err)
		return
	}
	items := make([]tokenInfo, 0, len(tokens))
	for _, t := range tokens {
		items = append(items, publicToken(t))
	}
	reply(w, 200, map[string]any{"tokens": items})
}
func (s *Server) issueToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Label  string  `json:"label"`
		Grants []scope `json:"grants"`
	}
	if !adminBody(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Label) == "" || len(in.Label) > 120 || len(in.Grants) == 0 || len(in.Grants) > 128 {
		invalidRequest(w)
		return
	}
	grants := make([]control.Grant, 0, len(in.Grants))
	records := map[string]control.Connection{}
	for _, g := range in.Grants {
		if g.DatasetID != "" {
			grant, err := s.datasetGrant(r.Context(), g)
			if err != nil {
				controlError(w, err)
				return
			}
			grants = append(grants, grant)
			continue
		}
		if g.Fields != nil || !connectionID.MatchString(g.ConnectionID) {
			invalidRequest(w)
			return
		}
		c, ok := records[g.ConnectionID]
		if !ok {
			var err error
			c, err = s.record(r.Context(), g.ConnectionID)
			if err != nil {
				controlError(w, err)
				return
			}
			records[c.ID] = c
		}
		if !slices.Contains(c.Databases[g.Database], g.Collection) {
			invalidRequest(w)
			return
		}
		grants = append(grants, control.Grant{ConnectionID: c.ID, Generation: c.Generation, Database: g.Database, Collection: g.Collection})
	}
	t, secret, err := s.admin.store.Issue(r.Context(), in.Label, grants)
	if err != nil {
		controlError(w, err)
		return
	}
	reply(w, 201, map[string]any{"token": publicToken(t), "secret": secret})
}
