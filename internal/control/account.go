package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/mail"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"golang.org/x/crypto/bcrypt"
)

const (
	RoleOwner  = "owner"
	RoleEditor = "editor"
	RoleViewer = "viewer"
)

type Account struct {
	ID                 string `json:"id" firestore:"id" bson:"_id"`
	Email              string `json:"email" firestore:"email" bson:"email"`
	Role               string `json:"role" firestore:"role" bson:"role"`
	Revision           string `json:"-" firestore:"revision" bson:"revision"`
	PasswordHash       []byte `json:"-" firestore:"passwordHash" bson:"passwordHash"`
	MustChangePassword bool   `json:"mustChangePassword" firestore:"mustChangePassword" bson:"mustChangePassword"`
	Disabled           bool   `json:"disabled" firestore:"disabled" bson:"disabled"`
	Primary            bool   `json:"primary" firestore:"primary" bson:"primary"`
	Operator           bool   `json:"operator" firestore:"-" bson:"-"`
}

type TeamStore interface {
	BootstrapAccount(context.Context, Account) error
	CreateAccount(context.Context, Account) error
	Accounts(context.Context) ([]Account, error)
	Account(context.Context, string) (Account, error)
	UpdateAccount(context.Context, Account, string) error
	MintAccountSession(context.Context, Account) (string, time.Time, error)
	SessionAccount(context.Context, string) (Account, error)
}

func ValidRole(role string) bool {
	return role == RoleOwner || role == RoleEditor || role == RoleViewer
}
func AccountID(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 {
		return "", ErrInvalid
	}
	sum := sha256.Sum256([]byte(email))
	return hex.EncodeToString(sum[:]), nil
}

func NewAccount(email, role string, primary bool) (Account, string, error) {
	password := hostedKey()
	a, err := NewAccountWithPassword(email, role, primary, password)
	if err != nil {
		return Account{}, "", err
	}
	return a, password, nil
}

func NewAccountWithPassword(email, role string, primary bool, password string) (Account, error) {
	id, err := AccountID(email)
	if err != nil || !ValidRole(role) || (primary && role != RoleOwner) {
		return Account{}, ErrInvalid
	}
	a := Account{ID: id, Email: strings.ToLower(strings.TrimSpace(email)), Role: role, Primary: primary}
	if err := a.SetPassword(password, true); err != nil {
		return Account{}, err
	}
	return a, nil
}

func (a *Account) SetPassword(password string, temporary bool) error {
	if len(password) < 12 || len(password) > 72 {
		return ErrInvalid
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	a.PasswordHash = hash
	a.MustChangePassword = temporary
	a.Revision = rand.Text()
	return nil
}

func (a Account) PasswordMatches(password string) bool {
	return len(password) <= 72 && bcrypt.CompareHashAndPassword(a.PasswordHash, []byte(password)) == nil
}

func (s *Store) accountDoc(id string) *firestore.DocumentRef {
	return s.db.Collection("consoleAccounts").Doc(id)
}

func (s *Store) BootstrapAccount(ctx context.Context, a Account) error {
	if !a.Primary || a.Role != RoleOwner || a.Disabled {
		return ErrInvalid
	}
	// The account and the installation-wide owner marker are created atomically.
	batch := s.db.Batch()
	batch.Create(s.db.Collection("consoleSettings").Doc("primaryOwner"), map[string]string{"accountId": a.ID})
	batch.Create(s.accountDoc(a.ID), a)
	_, err := batch.Commit(ctx)
	return storedError(err)
}

func (s *Store) CreateAccount(ctx context.Context, a Account) error {
	if a.Primary {
		return ErrInvalid
	}
	_, err := s.accountDoc(a.ID).Create(ctx, a)
	return storedError(err)
}

func decodeAccount(doc *firestore.DocumentSnapshot) (Account, error) {
	var a Account
	err := doc.DataTo(&a)
	if err == nil && (a.ID != doc.Ref.ID || a.Revision == "" || !ValidRole(a.Role)) {
		err = ErrInvalid
	}
	return a, err
}

func (s *Store) Accounts(ctx context.Context) ([]Account, error) {
	return documentList(ctx, s.db.Collection("consoleAccounts"), decodeAccount)
}

func (s *Store) Account(ctx context.Context, id string) (Account, error) {
	doc, err := s.accountDoc(id).Get(ctx)
	if err != nil {
		return Account{}, storedError(err)
	}
	return decodeAccount(doc)
}

func (s *Store) UpdateAccount(ctx context.Context, a Account, previousRevision string) error {
	if previousRevision == "" || a.Revision == previousRevision || a.Primary && (a.Role != RoleOwner || a.Disabled) {
		return ErrInvalid
	}
	ref := s.accountDoc(a.ID)
	err := s.db.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(ref)
		if err != nil {
			return err
		}
		prior, err := decodeAccount(doc)
		if err != nil {
			return err
		}
		if prior.Revision != previousRevision || a.Primary != prior.Primary {
			return ErrConflict
		}
		return tx.Set(ref, a)
	})
	return storedError(err)
}
