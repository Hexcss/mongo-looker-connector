package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Hexcss/mongo-looker-connector/internal/control"
	"golang.org/x/time/rate"
)

type accountContextKey struct{}

func currentAccount(r *http.Request) control.Account {
	a, _ := r.Context().Value(accountContextKey{}).(control.Account)
	return a
}

func (s *Server) accountInfo(w http.ResponseWriter, r *http.Request) {
	reply(w, 200, currentAccount(r))
}

// Named sessions may use only explicit method/path pairs. Reporting requests
// still require their own scoped token, even for team owners.
func consoleAccess(a control.Account, r *http.Request) bool {
	path := r.URL.Path
	if a.Disabled || !control.ValidRole(a.Role) {
		return false
	}
	if a.Operator && a.Role == control.RoleOwner {
		return true
	}
	if r.URL.EscapedPath() != path {
		return false
	}
	if r.Method == "GET" && (path == "/api/v1/admin/me" || path == "/admin/" || path == "/admin/console.css" || path == "/admin/console.js" || path == "/admin/icons.svg") {
		return true
	}
	if r.Method == "POST" && (path == "/api/v1/admin/end-session" || path == "/api/v1/admin/password") {
		return true
	}
	if a.MustChangePassword {
		return false
	}
	if path == "/api/v1/admin/bootstrap-owner" {
		return a.Operator
	}
	if a.Role == control.RoleOwner {
		return true
	}
	if r.Method == "GET" && path == "/api/v1/admin/datasets" {
		return true
	}
	if r.Method == "GET" && strings.HasPrefix(path, "/api/v1/admin/workspace/looker/") && connectionID.MatchString(strings.TrimPrefix(path, "/api/v1/admin/workspace/looker/")) {
		return true
	}
	if a.Role != control.RoleEditor {
		return false
	}
	switch r.Method {
	case "GET":
		return path == "/api/v1/admin/connections"
	case "POST":
		return path == "/api/v1/admin/preview" || path == "/api/v1/admin/datasets" || path == "/api/v1/admin/workspace/preview" || path == "/api/v1/admin/workspace/explain" || path == "/api/v1/admin/workspace/open"
	case "PUT":
		return strings.HasPrefix(path, "/api/v1/admin/datasets/") && connectionID.MatchString(strings.TrimPrefix(path, "/api/v1/admin/datasets/"))
	}
	return false
}

func administrativeRequest(w http.ResponseWriter, r *http.Request, a control.Account) (*http.Request, bool) {
	if !consoleAccess(a, r) {
		fail(w, 403, "forbidden", "Your account cannot perform this action.")
		return nil, false
	}
	return r.WithContext(context.WithValue(r.Context(), accountContextKey{}, a)), true
}

// Bounds public authentication cost and memory per gateway instance. The fixed
// buckets are keyed by normalized account ID (including unknown accounts).
type passwordGate struct {
	global  *rate.Limiter
	buckets [256]*rate.Limiter
	active  chan struct{}
	dummy   control.Account
}

func newPasswordGate() *passwordGate {
	g := &passwordGate{global: rate.NewLimiter(rate.Every(500*time.Millisecond), 20), active: make(chan struct{}, 4)}
	for i := range g.buckets {
		g.buckets[i] = rate.NewLimiter(rate.Every(10*time.Second), 8)
	}
	g.dummy, _, _ = control.NewAccount("unused@example.invalid", control.RoleViewer, false)
	return g
}

func (s *Server) enableTeam(store control.TeamStore) {
	s.admin.team = store
	gate := newPasswordGate()
	s.mux.HandleFunc("POST /api/v1/hosted/login", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var body struct{ Email, Password string }
		if !adminBody(w, r, &body) {
			return
		}
		id, err := control.AccountID(body.Email)
		hash := sha256.Sum256([]byte(id))
		if !gate.global.Allow() || !gate.buckets[hash[0]].Allow() {
			loginBusy(w)
			return
		}
		select {
		case gate.active <- struct{}{}:
			defer func() { <-gate.active }()
		default:
			loginBusy(w)
			return
		}
		var a control.Account
		if err == nil {
			a, err = store.Account(r.Context(), id)
		}
		if err != nil && !errors.Is(err, control.ErrNotFound) && !errors.Is(err, control.ErrInvalid) {
			controlError(w, err)
			return
		}
		candidate := a
		if err != nil || a.Disabled {
			candidate = gate.dummy
		}
		if !candidate.PasswordMatches(body.Password) || err != nil || a.Disabled {
			fail(w, 401, "unauthorized", "The email or password is incorrect.")
			return
		}
		raw, end, err := store.MintAccountSession(r.Context(), a)
		if err != nil {
			controlError(w, err)
			return
		}
		reply(w, 200, map[string]any{"session": raw, "expiresAt": end})
	})
	s.mux.HandleFunc("GET /api/v1/admin/users", func(w http.ResponseWriter, r *http.Request) {
		list, err := store.Accounts(r.Context())
		if err != nil {
			controlError(w, err)
			return
		}
		if list == nil {
			list = []control.Account{}
		}
		slices.SortFunc(list, func(a, b control.Account) int { return strings.Compare(a.Email, b.Email) })
		reply(w, 200, map[string]any{"users": list})
	})
	s.mux.HandleFunc("POST /api/v1/admin/bootstrap-owner", s.createAccount)
	s.mux.HandleFunc("POST /api/v1/admin/users", s.createAccount)
	s.mux.HandleFunc("PUT /api/v1/admin/users/{id}", s.modifyAccount)
	s.mux.HandleFunc("POST /api/v1/admin/users/{id}/reset-password", s.modifyAccount)
	s.mux.HandleFunc("POST /api/v1/admin/password", s.changePassword)
}

func loginBusy(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "10")
	fail(w, 429, "rate_limited", "Too many sign-in attempts. Try again shortly.")
}

func (s *Server) createAccount(w http.ResponseWriter, r *http.Request) {
	var body struct{ Email, Role string }
	if !adminBody(w, r, &body) {
		return
	}
	primary := r.URL.Path == "/api/v1/admin/bootstrap-owner"
	if primary {
		if !currentAccount(r).Operator {
			fail(w, 403, "forbidden", "Operator access is required for initialization.")
			return
		}
		body.Role = control.RoleOwner
	}
	a, password, err := control.NewAccount(body.Email, body.Role, primary)
	if err == nil {
		if primary {
			err = s.admin.team.BootstrapAccount(r.Context(), a)
		} else {
			err = s.admin.team.CreateAccount(r.Context(), a)
		}
	}
	if err != nil {
		controlError(w, err)
		return
	}
	reply(w, 201, map[string]any{"user": a, "temporaryPassword": password})
}

func (s *Server) modifyAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if len(id) != 64 || !connectionID.MatchString(id) {
		invalidRequest(w)
		return
	}
	me := currentAccount(r)
	if id == me.ID {
		fail(w, 403, "forbidden", "Use your profile to change your password.")
		return
	}
	a, err := s.admin.team.Account(r.Context(), id)
	if err != nil {
		controlError(w, err)
		return
	}
	old := a.Revision
	result := map[string]any{}
	if r.Method == "PUT" {
		var body struct {
			Role     string
			Disabled *bool
		}
		if !adminBody(w, r, &body) {
			return
		}
		if !control.ValidRole(body.Role) || body.Disabled == nil {
			invalidRequest(w)
			return
		}
		if a.Primary && (body.Role != control.RoleOwner || *body.Disabled) {
			fail(w, 403, "forbidden", "The primary owner must remain active.")
			return
		}
		if a.Role == body.Role && a.Disabled == *body.Disabled {
			reply(w, 200, map[string]any{"user": a})
			return
		}
		a.Role = body.Role
		a.Disabled = *body.Disabled
		a.Revision = rand.Text()
	} else {
		if a.Primary && !me.Operator {
			fail(w, 403, "forbidden", "Only the operator can reset the primary owner's password.")
			return
		}
		var body struct{}
		if !adminBody(w, r, &body) {
			return
		}
		password := rand.Text()
		if err = a.SetPassword(password, true); err != nil {
			controlError(w, err)
			return
		}
		result["temporaryPassword"] = password
	}
	if err = s.admin.team.UpdateAccount(r.Context(), a, old); err != nil {
		controlError(w, err)
		return
	}
	result["user"] = a
	reply(w, 200, result)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	a := currentAccount(r)
	if a.Operator || a.ID == "" {
		fail(w, 403, "forbidden", "Use a named console account to change its password.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var body struct{ CurrentPassword, NewPassword string }
	if !adminBody(w, r, &body) {
		return
	}
	if !a.PasswordMatches(body.CurrentPassword) {
		fail(w, 403, "forbidden", "The current password is incorrect.")
		return
	}
	if body.CurrentPassword == body.NewPassword {
		invalidRequest(w)
		return
	}
	old := a.Revision
	if err := a.SetPassword(body.NewPassword, false); err != nil {
		controlError(w, err)
		return
	}
	if err := s.admin.team.UpdateAccount(r.Context(), a, old); err != nil {
		controlError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
