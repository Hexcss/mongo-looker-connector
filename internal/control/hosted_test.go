package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
)

func TestHostedSessionLifecycle(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires Firestore emulator")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	db, err := firestore.NewClientWithDatabase(ctx, "mlc-test", fmt.Sprintf("login-%x", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := Store{db: db}
	end := time.Now().Add(15 * time.Minute).Truncate(time.Microsecond)
	raw, err := store.MintTicket(ctx, end)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(store.CheckSession(ctx, raw), ErrUnauthorized) {
		t.Fatal("ticket acted as a session")
	}
	type outcome struct {
		session string
		end     time.Time
		err     error
	}
	results := make(chan outcome, 2)
	for range 2 {
		go func() { key, until, err := store.ExchangeTicket(ctx, raw); results <- outcome{key, until, err} }()
	}
	var session string
	for range 2 {
		r := <-results
		if errors.Is(r.err, ErrUnauthorized) {
			continue
		}
		if r.err != nil || session != "" || !r.end.Equal(end) {
			t.Fatalf("invalid concurrent redemption: %v", r.err)
		}
		session = r.session
	}
	if err := store.CheckSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := store.EndSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(store.CheckSession(ctx, session), ErrUnauthorized) {
		t.Fatal("logged out session remained valid")
	}
	doc, _ := store.loginDoc("ticket", raw)
	if _, err = doc.Set(ctx, hostedLogin{ExpiresAt: time.Now().Add(-time.Hour), SessionEnd: end}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.ExchangeTicket(ctx, raw); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired ticket accepted: %v", err)
	}
	doc, _ = store.loginDoc("session", session)
	if _, err = doc.Set(ctx, hostedLogin{ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(store.CheckSession(ctx, session), ErrUnauthorized) {
		t.Fatal("expired session accepted")
	}
}
