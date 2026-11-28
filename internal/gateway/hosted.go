package gateway

import (
	"context"
	"net/http"
	"time"

	"github.com/Hexcss/mongo-looker-connector/internal/control"
	"google.golang.org/api/idtoken"
)

type loginStore interface {
	MintTicket(context.Context, time.Time) (string, error)
	ExchangeTicket(context.Context, string) (string, time.Time, error)
	CheckSession(context.Context, string) error
	EndSession(context.Context, string) error
}

func (s *Server) EnableHostedLogins(store loginStore) {
	s.admin.logins = store
	if team, ok := store.(control.TeamStore); ok {
		s.enableTeam(team)
	}
	s.mux.HandleFunc("POST /api/v1/admin/hosted-ticket", func(w http.ResponseWriter, r *http.Request) {
		// authorize has verified this assertion. A browser session cannot mint another session.
		claims, err := idtoken.ParsePayload(r.Header.Get("X-MLC-Admin-Assertion"))
		if err != nil {
			fail(w, 403, "forbidden", "Sign in with the operator account.")
			return
		}
		ticket, err := store.MintTicket(r.Context(), time.Unix(claims.Expires, 0))
		if err != nil {
			controlError(w, err)
			return
		}
		reply(w, 201, map[string]string{"ticket": ticket})
	})
	s.mux.HandleFunc("POST /api/v1/hosted/exchange", func(w http.ResponseWriter, r *http.Request) {
		key, end, err := store.ExchangeTicket(r.Context(), r.Header.Get("X-MLC-Login-Ticket"))
		if err != nil {
			controlError(w, err)
			return
		}
		reply(w, 200, map[string]any{"session": key, "expiresAt": end})
	})
	s.mux.HandleFunc("POST /api/v1/admin/end-session", func(w http.ResponseWriter, r *http.Request) {
		if err := store.EndSession(r.Context(), r.Header.Get("X-MLC-Hosted-Session")); err != nil {
			controlError(w, err)
			return
		}
		w.WriteHeader(204)
	})
}
