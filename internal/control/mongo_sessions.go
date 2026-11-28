package control

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (s *MongoStore) BootstrapAccount(ctx context.Context, account Account) error {
	if !account.Primary || account.Role != RoleOwner || account.Disabled {
		return ErrInvalid
	}
	return s.insertAccount(ctx, account)
}

func (s *MongoStore) CreateAccount(ctx context.Context, account Account) error {
	if account.Primary {
		return ErrInvalid
	}
	return s.insertAccount(ctx, account)
}

func (s *MongoStore) insertAccount(ctx context.Context, account Account) error {
	_, err := s.db.Collection("accounts").InsertOne(ctx, account)
	return mongoError(err)
}

func (s *MongoStore) Accounts(ctx context.Context) ([]Account, error) {
	return mongoList[Account](ctx, s.db.Collection("accounts"), bson.M{"passwordHash": 0})
}

func (s *MongoStore) Account(ctx context.Context, id string) (Account, error) {
	var account Account
	err := s.db.Collection("accounts").FindOne(ctx, bson.M{"_id": id}).Decode(&account)
	return account, mongoError(err)
}

func (s *MongoStore) UpdateAccount(ctx context.Context, account Account, revision string) error {
	if revision == "" || account.Revision == revision || account.Primary && (account.Disabled || account.Role != RoleOwner) {
		return ErrInvalid
	}
	c := s.db.Collection("accounts")
	filter := bson.M{"_id": account.ID, "revision": revision, "primary": account.Primary}
	err := c.FindOneAndReplace(ctx, filter, account, options.FindOneAndReplace().SetProjection(bson.M{"_id": 1})).Err()
	return revisionResult(ctx, c, account.ID, err)
}

type mongoSession struct {
	Key        string    `bson:"key"`
	ExpiresAt  time.Time `bson:"expiresAt"`
	SessionEnd time.Time `bson:"sessionEnd,omitempty"`
	AccountID  string    `bson:"accountId,omitempty"`
	Revision   string    `bson:"revision,omitempty"`
	Operator   bool      `bson:"operator,omitempty"`
}

func mongoSessionKey(kind, raw string) (string, error) {
	b, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(raw) != 43 || len(b) != 32 {
		return "", ErrUnauthorized
	}
	sum := sha256.Sum256(b)
	return kind + "-" + hex.EncodeToString(sum[:]), nil
}

func (s *MongoStore) MintTicket(ctx context.Context, assertionEnd time.Time) (string, error) {
	now := time.Now()
	if !assertionEnd.After(now) {
		return "", ErrUnauthorized
	}
	end := now.Add(time.Hour)
	if assertionEnd.Before(end) {
		end = assertionEnd
	}
	expires := now.Add(5 * time.Minute)
	if end.Before(expires) {
		expires = end
	}
	raw := hostedKey()
	key, _ := mongoSessionKey("ticket", raw)
	_, err := s.db.Collection("sessions").InsertOne(ctx, mongoSession{Key: key, ExpiresAt: expires, SessionEnd: end, Operator: true})
	return raw, err
}

func activeSessionFilter(kind, raw string) (bson.M, error) {
	key, err := mongoSessionKey(kind, raw)
	if err != nil {
		return nil, err
	}
	return bson.M{"key": key, "expiresAt": bson.M{"$gt": time.Now()}}, nil
}

func (s *MongoStore) ExchangeTicket(ctx context.Context, ticket string) (string, time.Time, error) {
	filter, err := activeSessionFilter("ticket", ticket)
	if err != nil {
		return "", time.Time{}, err
	}
	raw := hostedKey()
	key, _ := mongoSessionKey("session", raw)
	// Re-key the ticket in one document, so a standalone mongod can grant it
	// to exactly one caller without replica-set transactions.
	change := mongo.Pipeline{bson.D{{Key: "$set", Value: bson.M{"key": key, "expiresAt": "$sessionEnd"}}}}
	var row mongoSession
	err = s.db.Collection("sessions").FindOneAndUpdate(ctx, filter, change, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&row)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", time.Time{}, ErrUnauthorized
	}
	return raw, row.ExpiresAt, err
}

func (s *MongoStore) MintAccountSession(ctx context.Context, account Account) (string, time.Time, error) {
	if account.Disabled || account.ID == "" || account.Revision == "" || account.Operator || !ValidRole(account.Role) {
		return "", time.Time{}, ErrUnauthorized
	}
	raw := hostedKey()
	key, _ := mongoSessionKey("session", raw)
	end := time.Now().Add(time.Hour).UTC().Truncate(time.Millisecond)
	_, err := s.db.Collection("sessions").InsertOne(ctx, mongoSession{Key: key, ExpiresAt: end, AccountID: account.ID, Revision: account.Revision})
	return raw, end, err
}

func (s *MongoStore) SessionAccount(ctx context.Context, raw string) (Account, error) {
	filter, err := activeSessionFilter("session", raw)
	if err != nil {
		return Account{}, err
	}
	var row mongoSession
	err = s.db.Collection("sessions").FindOne(ctx, filter).Decode(&row)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Account{}, ErrUnauthorized
	}
	if err != nil {
		return Account{}, err
	}
	if row.Operator {
		return Account{Role: RoleOwner, Operator: true}, nil
	}
	if row.AccountID == "" || row.Revision == "" {
		return Account{}, ErrUnauthorized
	}
	var a Account
	err = s.db.Collection("accounts").FindOne(ctx, bson.M{"_id": row.AccountID, "revision": row.Revision, "disabled": false}).Decode(&a)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Account{}, ErrUnauthorized
	}
	return a, err
}

func (s *MongoStore) CheckSession(ctx context.Context, raw string) error {
	_, err := s.SessionAccount(ctx, raw)
	return err
}

func (s *MongoStore) EndSession(ctx context.Context, raw string) error {
	key, err := mongoSessionKey("session", raw)
	if err != nil {
		return err
	}
	_, err = s.db.Collection("sessions").DeleteOne(ctx, bson.M{"key": key})
	return err
}
