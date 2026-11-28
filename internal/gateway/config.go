package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

type connectionConfig struct {
	ID        string              `json:"id"`
	URIEnv    string              `json:"uriEnv"`
	Databases map[string][]string `json:"databases"`
}

type connection struct {
	generation     string
	client         *mongo.Client
	readPreference *readpref.ReadPref
	databases      map[string][]string
	timeout        time.Duration
}

// Driver debug logs can include complete commands, replies and server addresses.
// Keep them private even when an operator enables driver logging globally.
type discardDriverLogs struct{}

func (discardDriverLogs) Info(int, string, ...any)    {}
func (discardDriverLogs) Error(error, string, ...any) {}

type Server struct {
	admin       *administration
	mux         *http.ServeMux
	connections map[string]connection
}

var (
	connectionID  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	envName       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	invalidConfig = errors.New("invalid GATEWAY_CONNECTIONS configuration")
)

func validName(name string) bool {
	return name != "" && strings.TrimSpace(name) == name && !strings.ContainsRune(name, 0)
}

func (c connectionConfig) validate() error {
	if !connectionID.MatchString(c.ID) || !envName.MatchString(c.URIEnv) || len(c.Databases) == 0 {
		return invalidConfig
	}
	for database, names := range c.Databases {
		if !validName(database) || len(names) == 0 {
			return invalidConfig
		}
		slices.Sort(names)
		for i, name := range names {
			if !validName(name) || (i > 0 && name == names[i-1]) {
				return invalidConfig
			}
		}
	}
	return nil
}

// Open creates one driver pool per connection. The driver establishes database
// connectivity on demand, so one offline deployment cannot prevent startup.
func Open(raw, version, revision string) (*Server, error) {
	return open(raw, version, revision, false)
}

func open(raw, version, revision string, allowEmpty bool) (*Server, error) {
	var configs []connectionConfig
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&configs) != nil || configs == nil || (!allowEmpty && len(configs) == 0) || decoder.Decode(new(any)) != io.EOF {
		return nil, invalidConfig
	}
	s := &Server{mux: http.NewServeMux(), connections: make(map[string]connection, len(configs))}
	// Opening a later connection can fail after an earlier pool was created.
	opened := false
	defer func() {
		if !opened {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s.Close(ctx)
		}
	}()
	for _, cfg := range configs {
		if _, exists := s.connections[cfg.ID]; exists || cfg.validate() != nil {
			return nil, invalidConfig
		}
		uri := os.Getenv(cfg.URIEnv)
		if uri == "" {
			return nil, errors.New("configured MongoDB URI environment variable is empty")
		}
		c, err := openConnection(uri, cfg.Databases)
		if err != nil {
			return nil, err
		}
		c.generation = fmt.Sprintf("static:%x", sha256.Sum256([]byte(cfg.ID+"\x00"+uri)))
		s.connections[cfg.ID] = c
	}
	s.routes(version, revision)
	opened = true
	return s, nil
}

func (s *Server) Close(ctx context.Context) error {
	var err error
	for _, c := range s.connections {
		err = errors.Join(err, c.client.Disconnect(ctx))
	}
	return err
}

func openConnection(uri string, databases map[string][]string) (connection, error) {
	opts := options.Client().ApplyURI(uri).SetLoggerOptions(options.Logger().SetSink(discardDriverLogs{}))
	var timeout time.Duration
	if opts.Timeout != nil {
		timeout = *opts.Timeout
	}
	// Apply URI operation timeouts via our own contexts so discovery can
	// enforce the timeout locally without sending maxTimeMS to Firestore.
	client, err := mongo.Connect(opts.SetTimeout(0))
	if err != nil {
		// Driver errors can contain the original URI.
		return connection{}, errors.New("invalid MongoDB client configuration")
	}
	return connection{client: client, databases: databases, timeout: timeout, readPreference: opts.ReadPreference}, nil
}
