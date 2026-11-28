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

func TestNamedFirestoreSessions(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires Firestore emulator")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	db, err := firestore.NewClientWithDatabase(ctx, "mlc-test", fmt.Sprintf("team-%x", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := Store{db: db}
	owner, _, err := NewAccount("owner@example.invalid", RoleOwner, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.BootstrapAccount(ctx, owner); err != nil {
		t.Fatal(err)
	}
	another, _, _ := NewAccount("other@example.invalid", RoleOwner, true)
	if err = store.BootstrapAccount(ctx, another); !errors.Is(err, ErrConflict) {
		t.Fatal("second bootstrap accepted", err)
	}
	if _, err = store.Account(ctx, another.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("partial bootstrap", err)
	}
	a, _, _ := NewAccount("viewer@example.invalid", RoleViewer, false)
	if err = store.CreateAccount(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err = store.CreateAccount(ctx, a); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate account", err)
	}
	raw, _, err := store.MintAccountSession(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.SessionAccount(ctx, raw)
	if err != nil || user.ID != a.ID || user.Operator {
		t.Fatal("session identity", err)
	}
	old := a.Revision
	if err = a.SetPassword("my-new-passphrase", false); err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateAccount(ctx, a, old); err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateAccount(ctx, a, old); !errors.Is(err, ErrConflict) {
		t.Fatal("stale write", err)
	}
	if err = store.CheckSession(ctx, raw); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("stale session allowed", err)
	}
	raw, _, err = store.MintAccountSession(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	old = a.Revision
	a.Disabled = true
	a.Revision = "disabled-revision"
	if err = store.UpdateAccount(ctx, a, old); err != nil {
		t.Fatal(err)
	}
	if err = store.CheckSession(ctx, raw); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("disabled session allowed", err)
	}
	if got, err := store.Accounts(ctx); err != nil || len(got) != 2 {
		t.Fatal("account inventory", err)
	}
}
