package control

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Driver command logs can contain credentials or account records.
type silentMongoLogs struct{}

func (silentMongoLogs) Info(int, string, ...any)    {}
func (silentMongoLogs) Error(error, string, ...any) {}

type MongoStore struct {
	db     *mongo.Database
	cipher cipher.AEAD
}

// OpenMongo uses a dedicated database and a 32-byte key kept outside MongoDB.
func OpenMongo(ctx context.Context, uri, database string, key []byte) (*MongoStore, error) {
	if !connectionID.MatchString(database) || database == "admin" || database == "config" || database == "local" || len(key) != 32 {
		return nil, ErrInvalid
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetLoggerOptions(options.Logger().SetSink(silentMongoLogs{})).SetServerSelectionTimeout(5 * time.Second))
	if err != nil {
		return nil, errCredential
	}
	s := &MongoStore{db: client.Database(database), cipher: aead}
	if err = s.checkKey(ctx); err != nil {
		_ = s.Close()
		return nil, errors.New("cannot verify the MongoDB metadata encryption key")
	}
	if err = s.createIndexes(ctx); err != nil {
		_ = s.Close()
		return nil, errors.New("cannot initialize MongoDB metadata indexes")
	}
	return s, nil
}

// Pin the installation key on first use. A mismatched secret after a restore
// must fail at startup, before any writes could mix two encryption keys.
func (s *MongoStore) checkKey(ctx context.Context) error {
	const id = "encryption-key"
	aad := []byte(s.db.Name() + "/" + id)
	sealed := s.cipher.Seal(nil, nil, nil, aad)
	var row struct{ Check []byte }
	err := s.db.Collection("settings").FindOneAndUpdate(ctx, bson.M{"_id": id}, bson.M{"$setOnInsert": bson.M{"check": sealed}}, options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&row)
	if err != nil {
		return err
	}
	_, err = s.cipher.Open(nil, nil, row.Check, aad)
	return err
}

func (s *MongoStore) createIndexes(ctx context.Context) error {
	accounts := s.db.Collection("accounts").Indexes()
	_, err := accounts.CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "primary", Value: 1}}, Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"primary": true})},
	})
	if err != nil {
		return err
	}
	_, err = s.db.Collection("sessions").Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "key", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "expiresAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
	})
	return err
}

func (s *MongoStore) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.db.Client().Disconnect(ctx)
}

func mongoError(err error) error {
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return ErrNotFound
	case mongo.IsDuplicateKeyError(err):
		return ErrConflict
	default:
		return err
	}
}

func mongoList[T any](ctx context.Context, c *mongo.Collection, projection bson.M) ([]T, error) {
	opts := options.Find().SetSort(bson.D{{Key: "_id", Value: 1}})
	if projection != nil {
		opts.SetProjection(projection)
	}
	cursor, err := c.Find(ctx, bson.D{}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	items := []T{}
	if err := cursor.All(ctx, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func revisionResult(ctx context.Context, c *mongo.Collection, id string, err error) error {
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return mongoError(err)
	}
	// Distinguish a deleted resource from a stale revision without weakening CAS.
	if err := c.FindOne(ctx, bson.M{"_id": id}, options.FindOne().SetProjection(bson.M{"_id": 1})).Err(); err != nil {
		return mongoError(err)
	}
	return ErrConflict
}

func mongoDelete(ctx context.Context, c *mongo.Collection, id, revision string) error {
	if !connectionID.MatchString(id) || revision == "" {
		return ErrInvalid
	}
	err := c.FindOneAndDelete(ctx, bson.M{"_id": id, "revision": revision}, options.FindOneAndDelete().SetProjection(bson.M{"_id": 1})).Err()
	return revisionResult(ctx, c, id, err)
}

type mongoConnection struct {
	ID         string           `bson:"_id"`
	Databases  []databaseRecord `bson:"databases"`
	Generation string           `bson:"generation"`
	Revision   string           `bson:"revision"`
}

func (row mongoConnection) connection() Connection {
	c := Connection{ID: row.ID, Revision: row.Revision, Generation: row.Generation, Databases: make(map[string][]string, len(row.Databases))}
	for _, d := range row.Databases {
		c.Databases[d.Name] = d.Collections
	}
	return c
}

func (s *MongoStore) Connections(ctx context.Context) ([]Connection, error) {
	rows, err := mongoList[mongoConnection](ctx, s.db.Collection("connections"), bson.M{"credential": 0})
	if err != nil {
		return nil, err
	}
	items := make([]Connection, 0, len(rows))
	for _, row := range rows {
		items = append(items, row.connection())
	}
	return items, nil
}

func (s *MongoStore) GetConnection(ctx context.Context, id string) (Connection, error) {
	if !connectionID.MatchString(id) {
		return Connection{}, ErrInvalid
	}
	var row mongoConnection
	err := s.db.Collection("connections").FindOne(ctx, bson.M{"_id": id}, options.FindOne().SetProjection(bson.M{"credential": 0})).Decode(&row)
	return row.connection(), mongoError(err)
}

func (s *MongoStore) SaveConnection(ctx context.Context, id string, databases map[string][]string, uri, revision string) (Connection, error) {
	if !connectionID.MatchString(id) || revision == "" && uri == "" {
		return Connection{}, ErrInvalid
	}
	sorted, err := sortedDatabases(databases)
	if err != nil {
		return Connection{}, err
	}
	encoded := encodeConnection(Connection{Databases: sorted})
	next := Connection{ID: id, Databases: sorted, Revision: rand.Text()}
	values := bson.M{"databases": encoded.Databases, "revision": next.Revision}
	if uri != "" {
		if len(uri) > 64<<10 || !strings.HasPrefix(uri, "mongodb://") && !strings.HasPrefix(uri, "mongodb+srv://") {
			return Connection{}, ErrInvalid
		}
		next.Generation = rand.Text()
		values["generation"] = next.Generation
		values["credential"] = s.cipher.Seal(nil, nil, []byte(uri), []byte(id+"/"+next.Generation))
	}
	c := s.db.Collection("connections")
	if revision == "" {
		values["_id"] = id
		if _, err := c.InsertOne(ctx, values); err != nil {
			return Connection{}, mongoError(err)
		}
		return next, nil
	}
	var row mongoConnection
	err = c.FindOneAndUpdate(ctx, bson.M{"_id": id, "revision": revision}, bson.M{"$set": values}, options.FindOneAndUpdate().SetReturnDocument(options.After).SetProjection(bson.M{"credential": 0})).Decode(&row)
	return row.connection(), revisionResult(ctx, c, id, err)
}

func (s *MongoStore) DeleteConnection(ctx context.Context, id, revision string) error {
	return mongoDelete(ctx, s.db.Collection("connections"), id, revision)
}

func (s *MongoStore) URI(ctx context.Context, c Connection) (string, error) {
	var row struct{ Credential []byte }
	err := s.db.Collection("connections").FindOne(ctx, bson.M{"_id": c.ID, "generation": c.Generation}, options.FindOne().SetProjection(bson.M{"credential": 1})).Decode(&row)
	if err != nil {
		return "", mongoError(err)
	}
	data, err := s.cipher.Open(nil, nil, row.Credential, []byte(c.ID+"/"+c.Generation))
	if err != nil {
		return "", errCredential
	}
	return string(data), nil
}

func (s *MongoStore) Datasets(ctx context.Context) ([]Dataset, error) {
	return mongoList[Dataset](ctx, s.db.Collection("datasets"), nil)
}

func (s *MongoStore) GetDataset(ctx context.Context, id string) (Dataset, error) {
	if !connectionID.MatchString(id) {
		return Dataset{}, ErrInvalid
	}
	var d Dataset
	err := s.db.Collection("datasets").FindOne(ctx, bson.M{"_id": id}).Decode(&d)
	return d, mongoError(err)
}

func (s *MongoStore) SaveDataset(ctx context.Context, d Dataset) (Dataset, error) {
	if err := validateDataset(&d); err != nil {
		return Dataset{}, err
	}
	previous := d.Revision
	d.Revision = rand.Text()
	c := s.db.Collection("datasets")
	if previous == "" {
		_, err := c.InsertOne(ctx, d)
		return d, mongoError(err)
	}
	err := c.FindOneAndReplace(ctx, bson.M{"_id": d.ID, "revision": previous}, d, options.FindOneAndReplace().SetProjection(bson.M{"_id": 1})).Err()
	return d, revisionResult(ctx, c, d.ID, err)
}

func (s *MongoStore) DeleteDataset(ctx context.Context, id, revision string) error {
	return mongoDelete(ctx, s.db.Collection("datasets"), id, revision)
}

type mongoToken struct {
	Token  `bson:",inline"`
	Digest []byte `bson:"digest"`
}

func (s *MongoStore) Issue(ctx context.Context, label string, grants []Grant) (Token, string, error) {
	if err := validateGrants(label, grants); err != nil {
		return Token{}, "", err
	}
	row, raw := newBearer()
	row.Label, row.Grants, row.CreatedAt = label, grants, time.Now().UTC().Truncate(time.Millisecond)
	if _, err := s.db.Collection("tokens").InsertOne(ctx, mongoToken{row.Token, row.Digest}); err != nil {
		return Token{}, "", mongoError(err)
	}
	return row.Token, raw, nil
}

func (s *MongoStore) Tokens(ctx context.Context) ([]Token, error) {
	return mongoList[Token](ctx, s.db.Collection("tokens"), bson.M{"digest": 0})
}

func (s *MongoStore) Revoke(ctx context.Context, id string) error {
	if !tokenID.MatchString(id) {
		return ErrInvalid
	}
	return mongoError(s.db.Collection("tokens").FindOneAndUpdate(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"revoked": true}}, options.FindOneAndUpdate().SetProjection(bson.M{"_id": 1})).Err())
}

func (s *MongoStore) Authenticate(ctx context.Context, raw string) (Token, error) {
	id := bearerID(raw)
	if id == "" {
		return Token{}, ErrUnauthorized
	}
	var row mongoToken
	err := s.db.Collection("tokens").FindOne(ctx, bson.M{"_id": id}).Decode(&row)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Token{}, ErrUnauthorized
	}
	if err != nil {
		return Token{}, err
	}
	r := tokenRecord{Token: row.Token, Digest: row.Digest}
	if !r.matches(raw) {
		return Token{}, ErrUnauthorized
	}
	return row.Token, nil
}
