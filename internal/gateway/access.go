package gateway

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Hexcss/mongo-looker-connector/internal/control"
	"google.golang.org/api/idtoken"
)

type catalog interface {
	Datasets(context.Context) ([]control.Dataset, error)
	GetDataset(context.Context, string) (control.Dataset, error)
	SaveDataset(context.Context, control.Dataset) (control.Dataset, error)
	DeleteDataset(context.Context, string, string) error
	Connections(context.Context) ([]control.Connection, error)
	GetConnection(context.Context, string) (control.Connection, error)
	SaveConnection(context.Context, string, map[string][]string, string, string) (control.Connection, error)
	DeleteConnection(context.Context, string, string) error
	URI(context.Context, control.Connection) (string, error)
	Tokens(context.Context) ([]control.Token, error)
	Issue(context.Context, string, []control.Grant) (control.Token, string, error)
	Revoke(context.Context, string) error
	Authenticate(context.Context, string) (control.Token, error)
}

type administration struct {
	cache  resultCache
	logins loginStore
	store  catalog
	verify func(context.Context, string) bool
}

type tokenContextKey struct{}

func reportToken(r *http.Request) *control.Token {
	token, _ := r.Context().Value(tokenContextKey{}).(*control.Token)
	return token
}

// GoogleAdministrator verifies a separate, signed deployment-specific assertion.
// Cloud Run verifies the transport identity separately and may strip its signature.
func GoogleAdministrator(audience, email string) func(context.Context, string) bool {
	return func(ctx context.Context, proof string) bool {
		p, err := idtoken.Validate(ctx, proof, audience)
		return err == nil && (p.Issuer == "https://accounts.google.com" || p.Issuer == "accounts.google.com") &&
			p.Subject != "" && p.Claims["email"] == email && p.Claims["email_verified"] == true
	}
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	adminPath := r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/") || strings.HasPrefix(r.URL.Path, "/api/v1/admin/")
	proof := r.Header.Get("X-MLC-Admin-Assertion")
	raw := r.Header.Get("X-MLC-Token")
	if auth, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && strings.HasPrefix(auth, "mlc_") {
		if raw != "" {
			invalidRequest(w)
			return nil, false
		}
		raw = auth
	}
	// Exchange authenticates with the single-use ticket in its own handler.
	if s.admin.logins != nil && r.URL.Path == "/api/v1/hosted/exchange" && r.Method == "POST" {
		return r, true
	}
	if session := r.Header.Get("X-MLC-Hosted-Session"); session != "" {
		if s.admin.logins == nil || !adminPath || raw != "" || proof != "" || len(session) != 43 {
			fail(w, 401, "unauthorized", "Invalid browser session.")
			return nil, false
		}
		if err := s.admin.logins.CheckSession(r.Context(), session); err != nil {
			controlError(w, err)
			return nil, false
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-MLC-Request") != "console" {
			fail(w, 403, "forbidden", "The administrator request header is required.")
			return nil, false
		}
		return r, true
	}
	if proof != "" {
		if raw != "" || len(proof) > 8192 || !s.admin.verify(r.Context(), proof) {
			fail(w, 401, "unauthorized", "Invalid administrator credential.")
			return nil, false
		}
		if adminPath && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-MLC-Request") != "console" {
			fail(w, 403, "forbidden", "The administrator request header is required.")
			return nil, false
		}
		return r, true
	}
	if raw == "" || len(raw) > 512 {
		fail(w, 401, "unauthorized", "A reporting token or administrator credential is required.")
		return nil, false
	}
	token, err := s.admin.store.Authenticate(r.Context(), raw)
	if err != nil {
		controlError(w, err)
		return nil, false
	}
	if adminPath {
		fail(w, 403, "forbidden", "Administrator access is required.")
		return nil, false
	}
	return r.WithContext(context.WithValue(r.Context(), tokenContextKey{}, &token)), true
}

func (s *Server) records(ctx context.Context) ([]control.Connection, error) {
	records := make([]control.Connection, 0, len(s.connections))
	for id, c := range s.connections {
		records = append(records, control.Connection{ID: id, Generation: c.generation, Databases: c.databases})
	}
	if s.admin != nil {
		managed, err := s.admin.store.Connections(ctx)
		if err != nil {
			return nil, err
		}
		for _, c := range managed {
			if _, exists := s.connections[c.ID]; !exists {
				records = append(records, c)
			}
		}
	}
	slices.SortFunc(records, func(a, b control.Connection) int { return cmp.Compare(a.ID, b.ID) })
	return records, nil
}

func (s *Server) visibleIDs(r *http.Request) ([]string, error) {
	ids := []string{}
	token := reportToken(r)
	if token == nil {
		records, err := s.records(r.Context())
		if err != nil {
			return nil, err
		}
		for _, c := range records {
			ids = append(ids, c.ID)
		}
		if s.admin != nil {
			datasets, err := s.visibleDatasets(r)
			if err != nil {
				return nil, err
			}
			if len(datasets) != 0 {
				ids = append(ids, control.DatasetConnection)
				slices.Sort(ids)
			}
		}
		return ids, nil
	}
	distinct := map[string]bool{}
	for _, g := range token.Grants {
		distinct[g.ConnectionID] = true
	}
	for _, id := range slices.Sorted(maps.Keys(distinct)) {
		if id == control.DatasetConnection {
			datasets, err := s.visibleDatasets(r)
			if err != nil {
				return nil, err
			}
			if len(datasets) > 0 {
				ids = append(ids, id)
			}
			continue
		}
		c, err := s.record(r.Context(), id)
		if errors.Is(err, control.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if len(allowedDatabases(c, token)) > 0 {
			ids = append(ids, c.ID)
		}
	}
	return ids, nil
}

func (s *Server) record(ctx context.Context, id string) (control.Connection, error) {
	if c, ok := s.connections[id]; ok {
		return control.Connection{ID: id, Generation: c.generation, Databases: c.databases}, nil
	}
	if s.admin != nil && connectionID.MatchString(id) {
		return s.admin.store.GetConnection(ctx, id)
	}
	return control.Connection{}, control.ErrNotFound
}

func allowedDatabases(c control.Connection, token *control.Token) map[string][]string {
	if token == nil {
		return c.Databases
	}
	dbs := map[string][]string{}
	for _, g := range token.Grants {
		if g.ConnectionID == c.ID && g.Generation == c.Generation && slices.Contains(c.Databases[g.Database], g.Collection) {
			dbs[g.Database] = append(dbs[g.Database], g.Collection)
		}
	}
	for db := range dbs {
		slices.Sort(dbs[db])
	}
	return dbs
}

func (s *Server) resolve(r *http.Request, id, db, collection string) (connection, func(), error) {
	token := reportToken(r)
	if token != nil && !slices.ContainsFunc(token.Grants, func(g control.Grant) bool {
		return g.ConnectionID == id && (db == "" || db == g.Database) && (collection == "" || collection == g.Collection)
	}) {
		return connection{}, nil, control.ErrNotFound
	}
	meta, err := s.record(r.Context(), id)
	if err != nil {
		return connection{}, nil, err
	}
	visible := allowedDatabases(meta, token)
	if len(visible) == 0 || (db != "" && len(visible[db]) == 0) || (collection != "" && !slices.Contains(visible[db], collection)) {
		return connection{}, nil, control.ErrNotFound
	}
	return s.openRecord(r.Context(), meta, visible)
}

// Use the exact credential version checked during authorization for this request.
func (s *Server) openRecord(ctx context.Context, meta control.Connection, visible map[string][]string) (connection, func(), error) {
	if c, ok := s.connections[meta.ID]; ok {
		c.databases = visible
		return c, func() {}, nil
	}
	uri, err := s.admin.store.URI(ctx, meta)
	if err != nil {
		return connection{}, nil, err
	}
	// Request-owned pools never retain a retired credential across revisions.
	c, err := openConnection(uri, visible)
	if err != nil {
		return connection{}, nil, err
	}
	return c, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = c.client.Disconnect(ctx)
	}, nil
}

func controlError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, control.ErrInvalid):
		invalidRequest(w)
	case errors.Is(err, control.ErrNotFound):
		notConfigured(w)
	case errors.Is(err, control.ErrConflict):
		fail(w, 409, "conflict", "The resource changed. Refresh and try again.")
	case errors.Is(err, control.ErrUnauthorized):
		fail(w, 401, "unauthorized", "The reporting credential is invalid or revoked.")
	case errors.Is(err, context.DeadlineExceeded):
		fail(w, 504, "timeout", "The operation timed out.")
	default:
		fail(w, 503, "unavailable", "The configuration service is unavailable.")
	}
}
