package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/Hexcss/mongo-looker-connector/internal/engine"
)

func TestDatasetControlRejectsMalformedGrants(t *testing.T) {
	s := &Store{}
	g := Grant{ConnectionID: DatasetConnection, Database: DatasetDatabase, Collection: "orders", Generation: "revision", Fields: []string{"amount"}}
	for _, mutate := range []func(*Grant){
		func(g *Grant) { g.Fields = nil }, func(g *Grant) { g.Fields = []string{"x", "x"} }, func(g *Grant) { g.Database = "other" },
		func(g *Grant) { g.Collection = "bad/id" }, func(g *Grant) { g.ConnectionID = "raw" },
	} {
		bad := g
		mutate(&bad)
		if _, _, err := s.Issue(t.Context(), "t", []Grant{bad}); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	duplicate := g
	duplicate.Fields = []string{"other"}
	if _, _, err := s.Issue(t.Context(), "t", []Grant{g, duplicate}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestDatasetStoreEmulator(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("set FIRESTORE_EMULATOR_HOST to run the persistence test")
	}
	ctx := t.Context()
	db, err := firestore.NewClientWithDatabase(ctx, "mlc-test", fmt.Sprintf("datasets-%x", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Store{db: db}
	d := Dataset{ID: "__orders__", ConnectionID: "demo", Database: "sales", Collection: "orders", SourceGeneration: "source-gen", Pipeline: json.RawMessage(`[{"$sort":{"z":-1,"a":1}}]`), Fields: []engine.Field{{Name: "amount", Type: "NUMBER"}, {Name: "id", Type: "TEXT"}}}
	created, err := s.SaveDataset(ctx, d)
	if err != nil || created.Revision == "" {
		t.Fatal(err)
	}
	got, err := s.GetDataset(ctx, d.ID)
	if err != nil || !reflect.DeepEqual(got, created) {
		t.Fatalf("readback %+v, %v", got, err)
	}
	public, err := json.Marshal(got)
	if err != nil || bytes.Contains(public, []byte(d.SourceGeneration)) {
		t.Fatal("source generation leaked")
	}
	doc, err := s.dataset(d.ID).Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := doc.Data()["pipeline"].([]byte)
	if !ok || !bytes.Equal(stored, d.Pipeline) {
		t.Fatal("pipeline was not stored as ordered JSON bytes")
	}
	if _, err := s.SaveDataset(ctx, d); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate create: %v", err)
	}
	changed := created
	changed.Pipeline = json.RawMessage("[]")
	next, err := s.SaveDataset(ctx, changed)
	if err != nil || next.Revision == created.Revision {
		t.Fatalf("update %v", err)
	}
	if _, err := s.SaveDataset(ctx, created); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update: %v", err)
	}
	if err := s.DeleteDataset(ctx, d.ID, created.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale delete: %v", err)
	}
	list, err := s.Datasets(ctx)
	if err != nil || !reflect.DeepEqual(list, []Dataset{next}) {
		t.Fatalf("catalog: %+v %v", list, err)
	}
	g := Grant{ConnectionID: DatasetConnection, Database: DatasetDatabase, Collection: d.ID, Generation: next.Revision, Fields: []string{"amount"}}
	_, raw, err := s.Issue(ctx, "dataset reader", []Grant{g})
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.Authenticate(ctx, raw)
	if err != nil || !reflect.DeepEqual(token.Grants, []Grant{g}) {
		t.Fatalf("persisted dataset grant: %v", err)
	}
	if err := s.DeleteDataset(ctx, d.ID, next.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDataset(ctx, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	recreated, err := s.SaveDataset(ctx, d)
	if err != nil || recreated.Revision == next.Revision {
		t.Fatal("re-created dataset reused revision", err)
	}
}
