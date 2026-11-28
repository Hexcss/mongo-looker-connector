package control

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/Hexcss/mongo-looker-connector/internal/engine"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestMongoCatalogAndSessions(t *testing.T) {
	uri := os.Getenv("MLC_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set MLC_TEST_MONGO_URI to a disposable MongoDB server")
	}
	ctx := t.Context()
	key := make([]byte, 32)
	rand.Read(key)
	db := "mlc_test_" + rand.Text()
	s, err := OpenMongo(ctx, uri, db, key)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	defer s.db.Drop(ctx)
	wrong := make([]byte, 32)
	rand.Read(wrong)
	if other, err := OpenMongo(ctx, uri, db, wrong); err == nil {
		other.Close()
		t.Fatal("mismatched encryption key accepted")
	}
	names := map[string][]string{"an.example": []string{"orders"}}
	const source = "mongodb://reader:fixture-secret@source.example/analytics"
	c, err := s.SaveConnection(ctx, "source", names, source, "")
	if err != nil {
		t.Fatal(err)
	}
	actual, err := s.URI(ctx, c)
	if actual != source || err != nil {
		t.Fatal("URI readback")
	}
	raw, err := s.db.Collection("connections").FindOne(ctx, bson.M{"_id": "source"}).Raw()
	if err != nil || bytes.Contains(raw, []byte("fixture-secret")) {
		t.Fatal("credential stored in plaintext")
	}
	edited, err := s.SaveConnection(ctx, c.ID, names, "", c.Revision)
	if err != nil || edited.Generation != c.Generation {
		t.Fatal("credential lost on edit", err)
	}
	if _, err := s.SaveConnection(ctx, c.ID, names, "", c.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("stale edit", err)
	}
	rotated, err := s.SaveConnection(ctx, c.ID, names, source, edited.Revision)
	if err != nil || rotated.Generation == c.Generation {
		t.Fatal("rotation", err)
	}
	if _, err = s.URI(ctx, c); !errors.Is(err, ErrNotFound) {
		t.Fatal("old URI generation accepted", err)
	}

	d := Dataset{ID: "report", ConnectionID: "source", Database: "an.example", Collection: "orders", SourceGeneration: rotated.Generation, Pipeline: []byte(`[{"$sort":{"z":-1,"a":1}}]`), Fields: []engine.Field{{Name: "amount", Type: "NUMBER"}}}
	d, err = s.SaveDataset(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	actualDataset, err := s.GetDataset(ctx, d.ID)
	if !reflect.DeepEqual(actualDataset, d) || err != nil {
		t.Fatal("ordered dataset round trip", err)
	}
	raw, err = s.db.Collection("datasets").FindOne(ctx, bson.M{"_id": d.ID}).Raw()
	_, pipeline, ok := raw.Lookup("pipeline").BinaryOK()
	if err != nil || !ok || !bytes.Equal(pipeline, d.Pipeline) {
		t.Fatal("pipeline bytes changed")
	}
	g := Grant{ConnectionID: DatasetConnection, Database: DatasetDatabase, Collection: d.ID, Fields: []string{"amount"}, Generation: d.Revision}
	token, bearer, err := s.Issue(ctx, "read", []Grant{g})
	if err != nil {
		t.Fatal(err)
	}
	readToken, err := s.Authenticate(ctx, bearer)
	if err != nil || !reflect.DeepEqual(token, readToken) {
		t.Fatal("grant changed", err)
	}
	if err = s.Revoke(ctx, token.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(ctx, bearer); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("revoked grant", err)
	}

	a, _, err := NewAccount("owner@example.test", RoleOwner, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BootstrapAccount(ctx, a); err != nil {
		t.Fatal(err)
	}
	b, _, _ := NewAccount("other@example.test", RoleOwner, true)
	if err = s.BootstrapAccount(ctx, b); !errors.Is(err, ErrConflict) {
		t.Fatal("second primary accepted", err)
	}
	session, _, err := s.MintAccountSession(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if signed, err := s.SessionAccount(ctx, session); err != nil || signed.ID != a.ID {
		t.Fatal("named session", err)
	}
	prev := a.Revision
	if err = a.SetPassword("a new synthetic password", false); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateAccount(ctx, a, prev); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckSession(ctx, session); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("old session accepted", err)
	}

	ticket, err := s.MintTicket(ctx, time.Now().Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CheckSession(ctx, ticket); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("ticket acted as session", err)
	}
	results := make(chan string, 2)
	for range 2 {
		go func() {
			session, _, err := s.ExchangeTicket(ctx, ticket)
			if errors.Is(err, ErrUnauthorized) {
				results <- ""
				return
			}
			if err != nil {
				t.Error(err)
			}
			results <- session
		}()
	}
	var won []string
	for range 2 {
		if session := <-results; session != "" {
			won = append(won, session)
		}
	}
	if len(won) != 1 {
		t.Fatal("ticket redemption must be atomic")
	}
	if auth, err := s.SessionAccount(ctx, won[0]); err != nil || !auth.Operator || auth.Role != RoleOwner {
		t.Fatal("operator session", err)
	}
	if err = s.EndSession(ctx, won[0]); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckSession(ctx, won[0]); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("ended session accepted", err)
	}
}
