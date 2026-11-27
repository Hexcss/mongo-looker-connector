package control

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"strings"

	"cloud.google.com/go/firestore"
	"github.com/Hexcss/mongo-looker-connector/internal/engine"
)

// The virtual connection is outside the ID grammar for real MongoDB connections.
const DatasetConnection = "@datasets"
const DatasetDatabase = "published"

type Dataset struct {
	ID               string          `json:"id" firestore:"-"`
	ConnectionID     string          `json:"connectionId" firestore:"connectionId"`
	Database         string          `json:"database" firestore:"database"`
	Collection       string          `json:"collection" firestore:"collection"`
	Pipeline         json.RawMessage `json:"pipeline" firestore:"-"`
	Fields           []engine.Field  `json:"fields" firestore:"fields"`
	Revision         string          `json:"revision" firestore:"revision"`
	SourceGeneration string          `json:"-" firestore:"sourceGeneration"`
}

// Firestore treats named byte slices such as json.RawMessage as arrays of
// integers. Store the original JSON in one bytes value, preserving sort order.
type datasetRecord struct {
	Dataset
	PipelineBytes []byte `firestore:"pipeline"`
}

func (s *Store) dataset(id string) *firestore.DocumentRef {
	return s.db.Collection("datasets").Doc("id-" + id)
}

func decodeDataset(doc *firestore.DocumentSnapshot) (Dataset, error) {
	var row datasetRecord
	if err := doc.DataTo(&row); err != nil {
		return Dataset{}, err
	}
	d := row.Dataset
	d.Pipeline = row.PipelineBytes
	d.ID = strings.TrimPrefix(doc.Ref.ID, "id-")
	if d.Revision == "" || d.SourceGeneration == "" {
		return Dataset{}, ErrInvalid
	}
	return d, nil
}

func (s *Store) Datasets(ctx context.Context) ([]Dataset, error) {
	return documentList(ctx, s.db.Collection("datasets"), decodeDataset)
}

func (s *Store) GetDataset(ctx context.Context, id string) (Dataset, error) {
	if !connectionID.MatchString(id) {
		return Dataset{}, ErrInvalid
	}
	doc, err := s.dataset(id).Get(ctx)
	if err != nil {
		return Dataset{}, storedError(err)
	}
	return decodeDataset(doc)
}

func (s *Store) SaveDataset(ctx context.Context, d Dataset) (Dataset, error) {
	if !connectionID.MatchString(d.ID) || !connectionID.MatchString(d.ConnectionID) ||
		!validName(d.Database) || !validName(d.Collection) || !validName(d.SourceGeneration) {
		return Dataset{}, ErrInvalid
	}
	if len(d.Pipeline) == 0 {
		d.Pipeline = json.RawMessage("[]")
	}
	if err := engine.ValidateDataset(d.Pipeline, d.Fields); err != nil {
		return Dataset{}, ErrInvalid
	}
	previous := d.Revision
	d.Revision = rand.Text()
	ref := s.dataset(d.ID)
	row := datasetRecord{Dataset: d, PipelineBytes: d.Pipeline}
	err := s.db.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		if previous == "" {
			return tx.Create(ref, row)
		}
		if err := matchRevision(tx, ref, previous); err != nil {
			return err
		}
		return tx.Set(ref, row)
	})
	if err != nil {
		return Dataset{}, storedError(err)
	}
	return d, nil
}

func (s *Store) DeleteDataset(ctx context.Context, id, revision string) error {
	if !connectionID.MatchString(id) || revision == "" {
		return ErrInvalid
	}
	ref := s.dataset(id)
	err := s.db.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		if err := matchRevision(tx, ref, revision); err != nil {
			return err
		}
		return tx.Delete(ref)
	})
	return storedError(err)
}
