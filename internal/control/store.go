// Package control stores administration metadata in a dedicated Firestore Native
// database. Only Secret Manager receives the MongoDB credential.
package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"strings"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	sm "google.golang.org/api/secretmanager/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ErrInvalid      = errors.New("invalid control input")
	ErrNotFound     = errors.New("control resource not found")
	ErrConflict     = errors.New("control resource changed")
	ErrUnauthorized = errors.New("invalid reporting credential")

	connectionID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	secretID     = regexp.MustCompile(`^[A-Za-z0-9_-]{1,255}$`)
)

type Connection struct {
	ID         string              `json:"id"`
	Databases  map[string][]string `json:"databases"`
	Revision   string              `json:"revision"`
	Generation string              `json:"generation"`
	version    string
}

// MongoDB names are values rather than Firestore field paths. This also
// supports names such as "__archive__", which Firestore reserves as keys.
type databaseRecord struct {
	Name        string   `firestore:"name"`
	Collections []string `firestore:"collections"`
}

type connectionRecord struct {
	Revision   string           `firestore:"revision"`
	Generation string           `firestore:"generation"`
	Version    string           `firestore:"credentialVersion"`
	Databases  []databaseRecord `firestore:"databases"`
}

func encodeConnection(c Connection) connectionRecord {
	row := connectionRecord{Revision: c.Revision, Generation: c.Generation, Version: c.version}
	for _, name := range slices.Sorted(maps.Keys(c.Databases)) {
		row.Databases = append(row.Databases, databaseRecord{name, c.Databases[name]})
	}
	return row
}

type Store struct {
	db      *firestore.Client
	secrets *sm.Service
	secret  string
	owner   string
	cancel  context.CancelFunc
	done    chan struct{}
}

func Open(ctx context.Context, project, database, secret string) (*Store, error) {
	if !validName(project) || !validName(database) || strings.Contains(project+database, "/") || database == "(default)" {
		return nil, ErrInvalid
	}
	prefix := "projects/" + project + "/secrets/"
	id := strings.TrimPrefix(secret, prefix)
	if !secretID.MatchString(id) {
		return nil, ErrInvalid
	}
	// The generated Google clients can log request and response bodies at debug level.
	quiet := option.WithLogger(slog.New(slog.DiscardHandler))
	db, err := firestore.NewClientWithDatabase(ctx, project, database, quiet)
	if err != nil {
		return nil, err
	}
	secrets, err := sm.NewService(ctx, quiet)
	if err != nil {
		db.Close()
		return nil, err
	}
	worker, cancel := context.WithCancel(context.Background())
	s := &Store{db: db, secrets: secrets, secret: prefix + id, owner: project + "/" + database, cancel: cancel, done: make(chan struct{})}
	go s.cleanupLoop(worker)
	return s, nil
}

func (s *Store) Close() error {
	s.cancel()
	<-s.done
	return s.db.Close()
}

func validName(name string) bool {
	return name != "" && name == strings.TrimSpace(name) && !strings.ContainsRune(name, 0)
}

func (s *Store) connection(id string) *firestore.DocumentRef {
	// The prefix also makes Firestore's reserved "__...__" IDs available.
	return s.db.Collection("connections").Doc("id-" + id)
}

func sortedDatabases(databases map[string][]string) (map[string][]string, error) {
	if len(databases) == 0 {
		return nil, ErrInvalid
	}
	result := make(map[string][]string, len(databases))
	for db, collections := range databases {
		if !validName(db) || len(collections) == 0 {
			return nil, ErrInvalid
		}
		names := slices.Clone(collections)
		slices.Sort(names)
		for i, name := range names {
			if !validName(name) || (i > 0 && name == names[i-1]) {
				return nil, ErrInvalid
			}
		}
		result[db] = names
	}
	return result, nil
}

func storedError(err error) error {
	switch status.Code(err) {
	case codes.NotFound:
		return ErrNotFound
	case codes.AlreadyExists, codes.FailedPrecondition, codes.Aborted:
		return ErrConflict
	default:
		return err
	}
}

func documentList[T any](ctx context.Context, collection *firestore.CollectionRef, decode func(*firestore.DocumentSnapshot) (T, error)) ([]T, error) {
	it := collection.Documents(ctx)
	defer it.Stop()
	items := []T{}
	for {
		doc, err := it.Next()
		if err == iterator.Done {
			return items, nil
		}
		if err != nil {
			return nil, err
		}
		item, err := decode(doc)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
}

func decodeConnection(doc *firestore.DocumentSnapshot) (Connection, error) {
	var row connectionRecord
	if err := doc.DataTo(&row); err != nil {
		return Connection{}, err
	}
	if row.Generation == "" || row.Revision == "" || row.Version == "" {
		return Connection{}, errors.New("incomplete stored connection")
	}
	c := Connection{ID: strings.TrimPrefix(doc.Ref.ID, "id-"), Revision: row.Revision, Generation: row.Generation, version: row.Version, Databases: make(map[string][]string, len(row.Databases))}
	for _, db := range row.Databases {
		c.Databases[db.Name] = db.Collections
	}
	return c, nil
}

func (s *Store) Connections(ctx context.Context) ([]Connection, error) {
	return documentList(ctx, s.db.Collection("connections"), decodeConnection)
}

func (s *Store) GetConnection(ctx context.Context, id string) (Connection, error) {
	if !connectionID.MatchString(id) {
		return Connection{}, ErrInvalid
	}
	doc, err := s.connection(id).Get(ctx)
	if err != nil {
		return Connection{}, storedError(err)
	}
	return decodeConnection(doc)
}

func (s *Store) SaveConnection(ctx context.Context, id string, databases map[string][]string, uri, revision string) (Connection, error) {
	if !connectionID.MatchString(id) || (revision == "" && uri == "") {
		return Connection{}, ErrInvalid
	}
	databases, err := sortedDatabases(databases)
	if err != nil {
		return Connection{}, err
	}
	current, err := s.GetConnection(ctx, id)
	if errors.Is(err, ErrNotFound) && revision != "" {
		return Connection{}, ErrNotFound
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Connection{}, err
	}
	if current.Revision != revision {
		return Connection{}, ErrConflict
	}

	next := Connection{ID: id, Databases: databases, Revision: rand.Text(), Generation: current.Generation, version: current.version}
	if uri != "" {
		next.Generation = rand.Text()
		next.version, err = s.addURI(ctx, uri)
		if err != nil {
			return Connection{}, err
		}
	}
	ref := s.connection(id)
	err = s.db.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		if revision != "" {
			if err := matchRevision(tx, ref, revision); err != nil {
				return err
			}
		}
		if uri != "" {
			// The create-only claim also fences a late publisher if cleanup won.
			if err := tx.Create(s.claim(next.version), map[string]any{"retired": false}); err != nil {
				return err
			}
			if current.version != "" {
				if err := s.retire(tx, current.version); err != nil {
					return err
				}
			}
		}
		row := encodeConnection(next)
		if revision == "" {
			return tx.Create(ref, row)
		}
		return tx.Set(ref, row)
	})
	if err != nil {
		// A timeout can follow a successful commit. The collector reconciles the
		// immutable version against its atomic claim before destroying anything.
		return Connection{}, storedError(err)
	}
	return next, nil
}

func matchRevision(tx *firestore.Transaction, ref *firestore.DocumentRef, revision string) error {
	doc, err := tx.Get(ref)
	if err != nil {
		return storedError(err)
	}
	current, err := doc.DataAt("revision")
	if err != nil {
		return err
	}
	if current != revision {
		return ErrConflict
	}
	return nil
}

func (s *Store) DeleteConnection(ctx context.Context, id, revision string) error {
	if !connectionID.MatchString(id) || revision == "" {
		return ErrInvalid
	}
	ref := s.connection(id)
	err := s.db.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(ref)
		if err != nil {
			return storedError(err)
		}
		current, err := decodeConnection(doc)
		if err != nil {
			return err
		}
		if current.Revision != revision {
			return ErrConflict
		}
		if err := s.retire(tx, current.version); err != nil {
			return err
		}
		return tx.Delete(ref)
	})
	return storedError(err)
}

// A retired claim never becomes active again, even after its payload is destroyed.
func (s *Store) retire(tx *firestore.Transaction, version string) error {
	return tx.Update(s.claim(version), []firestore.Update{{Path: "retired", Value: true}})
}

func (s *Store) claim(version string) *firestore.DocumentRef {
	hash := sha256.Sum256([]byte(version))
	return s.db.Collection("credentialVersions").Doc(hex.EncodeToString(hash[:]))
}
