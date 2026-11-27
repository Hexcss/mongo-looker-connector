package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
)

type Grant struct {
	Fields       []string `json:"fields,omitempty" firestore:"fields,omitempty"`
	ConnectionID string   `json:"connectionId" firestore:"connectionId"`
	Database     string   `json:"database" firestore:"database"`
	Collection   string   `json:"collection" firestore:"collection"`
	Generation   string   `json:"generation,omitempty" firestore:"generation"`
}

type Token struct {
	ID        string    `json:"id" firestore:"-"`
	Label     string    `json:"label" firestore:"label"`
	Grants    []Grant   `json:"grants" firestore:"grants"`
	CreatedAt time.Time `json:"createdAt" firestore:"createdAt"`
	Revoked   bool      `json:"revoked" firestore:"revoked"`
}

type tokenRecord struct {
	Token
	Digest []byte `firestore:"digest" json:"-"`
}

var tokenID = regexp.MustCompile(`^[0-9a-f]{32}$`)

func newBearer() (tokenRecord, string) {
	var id [16]byte
	var secret [32]byte
	rand.Read(id[:])
	rand.Read(secret[:])
	record := tokenRecord{Token: Token{ID: hex.EncodeToString(id[:])}}
	raw := "mlc_" + record.ID + "." + base64.RawURLEncoding.EncodeToString(secret[:])
	hash := sha256.Sum256([]byte(raw))
	record.Digest = hash[:]
	return record, raw
}

func bearerID(raw string) string {
	if len(raw) != 4+32+1+43 || !strings.HasPrefix(raw, "mlc_") || raw[36] != '.' {
		return ""
	}
	id := raw[4:36]
	if !tokenID.MatchString(id) {
		return ""
	}
	if secret, err := base64.RawURLEncoding.Strict().DecodeString(raw[37:]); err != nil || len(secret) != 32 {
		return ""
	}
	return id
}

func (r tokenRecord) matches(raw string) bool {
	hash := sha256.Sum256([]byte(raw))
	return !r.Revoked && subtle.ConstantTimeCompare(r.Digest, hash[:]) == 1
}

func (s *Store) Issue(ctx context.Context, label string, grants []Grant) (Token, string, error) {
	if !validName(label) || len(label) > 128 || len(grants) == 0 || len(grants) > 256 {
		return Token{}, "", ErrInvalid
	}
	seen := make(map[[3]string]bool, len(grants))
	for _, g := range grants {
		key := [3]string{g.ConnectionID, g.Database, g.Collection}
		if !validName(g.Database) || !validName(g.Collection) || !validName(g.Generation) || seen[key] {
			return Token{}, "", ErrInvalid
		}
		if g.ConnectionID == DatasetConnection {
			if g.Database != DatasetDatabase || !connectionID.MatchString(g.Collection) || len(g.Fields) == 0 || len(g.Fields) > 256 {
				return Token{}, "", ErrInvalid
			}
			names := map[string]bool{}
			for _, name := range g.Fields {
				if !validName(name) || len(name) > 512 || names[name] {
					return Token{}, "", ErrInvalid
				}
				names[name] = true
			}
		} else if !connectionID.MatchString(g.ConnectionID) || len(g.Fields) != 0 {
			return Token{}, "", ErrInvalid
		}
		seen[key] = true
	}
	row, raw := newBearer()
	row.Label = label
	row.Grants = grants
	row.CreatedAt = time.Now().UTC().Truncate(time.Microsecond)
	_, err := s.db.Collection("tokens").Doc(row.ID).Create(ctx, row)
	if err != nil {
		return Token{}, "", storedError(err)
	}
	return row.Token, raw, nil
}

func (s *Store) Tokens(ctx context.Context) ([]Token, error) {
	return documentList(ctx, s.db.Collection("tokens"), func(doc *firestore.DocumentSnapshot) (Token, error) {
		var t Token
		err := doc.DataTo(&t)
		t.ID = doc.Ref.ID
		return t, err
	})
}

func (s *Store) Revoke(ctx context.Context, id string) error {
	if !tokenID.MatchString(id) {
		return ErrInvalid
	}
	_, err := s.db.Collection("tokens").Doc(id).Update(ctx, []firestore.Update{{Path: "revoked", Value: true}})
	return storedError(err)
}

func (s *Store) Authenticate(ctx context.Context, raw string) (Token, error) {
	id := bearerID(raw)
	if id == "" {
		return Token{}, ErrUnauthorized
	}
	doc, err := s.db.Collection("tokens").Doc(id).Get(ctx)
	if errors.Is(storedError(err), ErrNotFound) {
		return Token{}, ErrUnauthorized
	}
	if err != nil {
		return Token{}, err
	}
	var row tokenRecord
	if err := doc.DataTo(&row); err != nil {
		return Token{}, err
	}
	if !row.matches(raw) {
		return Token{}, ErrUnauthorized
	}
	row.ID = id
	return row.Token, nil
}
