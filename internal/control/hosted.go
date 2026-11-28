package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"cloud.google.com/go/firestore"
)

// Firestore TTL clears abandoned records. Every authorization checks expiration
// directly because TTL deletion is asynchronous.
type hostedLogin struct {
	AccountID  string    `firestore:"accountId,omitempty"`
	Revision   string    `firestore:"accountRevision,omitempty"`
	ExpiresAt  time.Time `firestore:"expiresAt"`
	SessionEnd time.Time `firestore:"sessionEnd,omitempty"`
}

func hostedKey() string {
	var b [32]byte
	rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func (s *Store) loginDoc(kind, raw string) (*firestore.DocumentRef, error) {
	data, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if len(raw) != 43 || err != nil || len(data) != 32 {
		return nil, ErrUnauthorized
	}
	sum := sha256.Sum256(data)
	return s.db.Collection("hostedLogins").Doc(kind + "-" + hex.EncodeToString(sum[:])), nil
}

func (s *Store) MintTicket(ctx context.Context, assertionEnd time.Time) (string, error) {
	now := time.Now()
	if !assertionEnd.After(now) {
		return "", ErrUnauthorized
	}
	sessionEnd := now.Add(time.Hour)
	if assertionEnd.Before(sessionEnd) {
		sessionEnd = assertionEnd
	}
	end := now.Add(5 * time.Minute)
	if sessionEnd.Before(end) {
		end = sessionEnd
	}
	raw := hostedKey()
	doc, _ := s.loginDoc("ticket", raw)
	_, err := doc.Create(ctx, hostedLogin{ExpiresAt: end, SessionEnd: sessionEnd})
	return raw, err
}

func activeLogin(snapshot *firestore.DocumentSnapshot, err error) (hostedLogin, error) {
	var row hostedLogin
	if errors.Is(storedError(err), ErrNotFound) {
		return row, ErrUnauthorized
	}
	if err != nil {
		return row, err
	}
	if err = snapshot.DataTo(&row); err == nil && !time.Now().Before(row.ExpiresAt) {
		err = ErrUnauthorized
	}
	return row, err
}

func (s *Store) ExchangeTicket(ctx context.Context, ticket string) (string, time.Time, error) {
	doc, err := s.loginDoc("ticket", ticket)
	if err != nil {
		return "", time.Time{}, err
	}
	session := hostedKey()
	sessionDoc, _ := s.loginDoc("session", session)
	var end time.Time
	err = s.db.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		row, err := activeLogin(tx.Get(doc))
		if err != nil {
			return err
		}
		end = row.SessionEnd
		if err = tx.Delete(doc); err != nil {
			return err
		}
		return tx.Create(sessionDoc, hostedLogin{ExpiresAt: end})
	})
	return session, end, err
}

func (s *Store) MintAccountSession(ctx context.Context, a Account) (string, time.Time, error) {
	if a.ID == "" || a.Revision == "" || a.Disabled || a.Operator || !ValidRole(a.Role) {
		return "", time.Time{}, ErrUnauthorized
	}
	end := time.Now().Add(time.Hour)
	raw := hostedKey()
	doc, _ := s.loginDoc("session", raw)
	_, err := doc.Create(ctx, hostedLogin{ExpiresAt: end, AccountID: a.ID, Revision: a.Revision})
	return raw, end, err
}

func (s *Store) SessionAccount(ctx context.Context, session string) (Account, error) {
	doc, err := s.loginDoc("session", session)
	if err != nil {
		return Account{}, err
	}
	row, err := activeLogin(doc.Get(ctx))
	if err != nil {
		return Account{}, err
	}
	// Operator sessions issued before named accounts have no account binding.
	if row.AccountID == "" && row.Revision == "" {
		return Account{Role: RoleOwner, Operator: true}, nil
	}
	if row.AccountID == "" || row.Revision == "" {
		return Account{}, ErrUnauthorized
	}
	a, err := s.Account(ctx, row.AccountID)
	if errors.Is(err, ErrNotFound) || (err == nil && (a.Disabled || a.Revision != row.Revision)) {
		return Account{}, ErrUnauthorized
	}
	return a, err
}

func (s *Store) CheckSession(ctx context.Context, session string) error {
	_, err := s.SessionAccount(ctx, session)
	return err
}

func (s *Store) EndSession(ctx context.Context, session string) error {
	doc, err := s.loginDoc("session", session)
	if err != nil {
		return err
	}
	_, err = doc.Delete(ctx)
	return err
}
