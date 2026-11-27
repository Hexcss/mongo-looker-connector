package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"hash/crc32"
	"log"
	"strconv"
	"strings"
	"time"

	sm "google.golang.org/api/secretmanager/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const credentialGrace = 15 * time.Minute

var (
	castagnoli        = crc32.MakeTable(crc32.Castagnoli)
	errCredential     = errors.New("credential storage unavailable")
	errForeignPayload = errors.New("unrecognized credential payload")
)

type credential struct {
	Owner string `json:"controlDatabase"`
	URI   string `json:"uri"`
}

// Google may return the project number when the request used its project ID.
// Rebuild the pinned resource under the configured secret, never an alias.
func (s *Store) pinned(name string) (string, error) {
	parts := strings.Split(name, "/")
	parent := strings.Split(s.secret, "/")
	if len(parent) != 4 || len(parts) != 6 || parts[0] != "projects" || parts[2] != "secrets" || parts[3] != parent[3] || parts[4] != "versions" {
		return "", errCredential
	}
	if parts[1] != parent[1] {
		if _, err := strconv.ParseUint(parts[1], 10, 64); err != nil {
			return "", errCredential
		}
	}
	n, err := strconv.ParseUint(parts[5], 10, 64)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != parts[5] {
		return "", errCredential
	}
	return s.secret + "/versions/" + parts[5], nil
}

func (s *Store) addURI(ctx context.Context, uri string) (string, error) {
	if !strings.HasPrefix(uri, "mongodb://") && !strings.HasPrefix(uri, "mongodb+srv://") {
		return "", ErrInvalid
	}
	data, err := json.Marshal(credential{Owner: s.owner, URI: uri})
	if err != nil || len(data) > 64<<10 {
		return "", ErrInvalid
	}
	payload := &sm.SecretPayload{Data: base64.StdEncoding.EncodeToString(data), DataCrc32c: int64(crc32.Checksum(data, castagnoli)), ForceSendFields: []string{"DataCrc32c"}}
	version, err := s.secrets.Projects.Secrets.AddVersion(s.secret, &sm.AddSecretVersionRequest{Payload: payload}).Context(ctx).Do()
	if err != nil {
		return "", errCredential
	}
	return s.pinned(version.Name)
}

func (s *Store) readURI(ctx context.Context, version string) (string, error) {
	pin, err := s.pinned(version)
	if err != nil || pin != version {
		return "", errCredential
	}
	response, err := s.secrets.Projects.Secrets.Versions.Access(pin).Context(ctx).Do()
	if err != nil || response.Payload == nil {
		return "", errCredential
	}
	data, err := base64.StdEncoding.DecodeString(response.Payload.Data)
	if err != nil || int64(crc32.Checksum(data, castagnoli)) != response.Payload.DataCrc32c {
		return "", errCredential
	}
	var value credential
	if json.Unmarshal(data, &value) != nil || value.Owner != s.owner || value.URI == "" {
		return "", errForeignPayload
	}
	return value.URI, nil
}

func (s *Store) URI(ctx context.Context, c Connection) (string, error) {
	return s.readURI(ctx, c.version)
}

func (s *Store) cleanupLoop(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		work, cancel := context.WithTimeout(ctx, time.Minute)
		err := s.cleanup(work, time.Now())
		cancel()
		if err != nil && ctx.Err() == nil {
			// Upstream SDK errors may include payloads or account identifiers.
			log.Print("credential version cleanup incomplete, retry scheduled")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// A crash between adding a version and committing its metadata can leave an
// orphan. Claim creation races atomically with publication. A retired claim
// is permanent so even an arbitrarily delayed publisher cannot revive it.
func (s *Store) cleanup(ctx context.Context, now time.Time) error {
	before := now.Add(-credentialGrace)
	filter := "state:(ENABLED OR DISABLED) AND create_time<" + before.UTC().Format(time.RFC3339)
	versions := s.secrets.Projects.Secrets.Versions
	var failures error
	err := versions.List(s.secret).Filter(filter).Pages(ctx, func(page *sm.ListSecretVersionsResponse) error {
		for _, candidate := range page.Versions {
			if candidate.ScheduledDestroyTime != "" {
				continue
			}
			if err := s.cleanupVersion(ctx, candidate, before); err != nil {
				failures = errors.Join(failures, err)
			}
		}
		return ctx.Err()
	})
	return errors.Join(err, failures)
}

func (s *Store) cleanupVersion(ctx context.Context, candidate *sm.SecretVersion, before time.Time) error {
	version, err := s.pinned(candidate.Name)
	if err != nil {
		return err
	}
	ref := s.claim(version)
	doc, err := ref.Get(ctx)
	switch status.Code(err) {
	case codes.OK:
		retired, err := doc.DataAt("retired")
		if err != nil {
			return err
		}
		if retired != true || !doc.UpdateTime.Before(before) {
			return nil
		}
	case codes.NotFound:
		if candidate.State != "ENABLED" {
			return nil
		}
		if _, err := s.readURI(ctx, version); err != nil {
			if errors.Is(err, errForeignPayload) {
				return nil
			}
			return err
		}
		_, err = ref.Create(ctx, map[string]any{"retired": true})
		if status.Code(err) == codes.AlreadyExists {
			return nil
		}
		if err != nil {
			return err
		}
	default:
		return err
	}
	_, err = s.secrets.Projects.Secrets.Versions.Destroy(version, &sm.DestroySecretVersionRequest{}).Context(ctx).Do()
	return err
}
