package main

import (
	"context"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Hexcss/mongo-looker-connector/internal/console"
	"github.com/Hexcss/mongo-looker-connector/internal/control"
	"github.com/Hexcss/mongo-looker-connector/internal/gateway"
	"github.com/Hexcss/mongo-looker-connector/internal/hosted"
)

// Release builds populate these with -ldflags.
var version = "dev"
var revision = "unknown"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	if len(os.Args) > 1 {
		if len(os.Args) == 2 && os.Args[1] == "init-owner" {
			return initMongoOwner(ctx)
		}
		return errors.New("unknown gateway command")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return errors.New("PORT must be a valid TCP port")
	}
	var handler http.Handler
	if url := os.Getenv("MLC_STANDALONE_UPSTREAM"); url != "" {
		if os.Getenv("MLC_HOSTED_GATEWAY") != "" {
			return errors.New("configure one hosted gateway upstream")
		}
		proxy, err := hosted.OpenStandalone(url)
		if err != nil {
			return err
		}
		handler = proxy
	} else if url := os.Getenv("MLC_HOSTED_GATEWAY"); url != "" {
		proxy, err := hosted.Open(ctx, url)
		if err != nil {
			return err
		}
		handler = proxy
	} else {
		api, closeStore, err := openGateway(ctx)
		if err != nil {
			return err
		}
		defer closeStore()
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if api.Close(closeCtx) != nil {
				log.Print("database pool shutdown failed")
			}
		}()
		handler = api
	}
	server := &http.Server{
		Addr: ":" + port, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 32 << 10,
	}
	stopped := make(chan error, 1)
	go func() { stopped <- server.ListenAndServe() }()
	select {
	case err := <-stopped:
		if !errors.Is(err, http.ErrServerClosed) {
			return errors.New("HTTP listener failed")
		}
	case <-ctx.Done():
		drain, cancel := context.WithTimeout(context.Background(), 7*time.Second)
		defer cancel()
		if err := server.Shutdown(drain); err != nil {
			_ = server.Close()
			return errors.New("HTTP shutdown timed out")
		}
	}
	return nil
}

func openGateway(ctx context.Context) (*gateway.Server, func(), error) {
	project, db, secret := os.Getenv("MLC_ADMIN_PROJECT"), os.Getenv("MLC_ADMIN_DATABASE"), os.Getenv("MLC_ADMIN_SECRET")
	email, aud := os.Getenv("MLC_ADMIN_EMAIL"), os.Getenv("MLC_ADMIN_AUDIENCE")
	raw := os.Getenv("GATEWAY_CONNECTIONS")
	if os.Getenv("MLC_CONTROL_MONGO_URI_FILE") != "" || os.Getenv("MLC_CONTROL_MONGO_KEY_FILE") != "" || os.Getenv("MLC_CONTROL_MONGO_DATABASE") != "" {
		if project != "" || db != "" || secret != "" || (email == "") != (aud == "") {
			return nil, nil, errors.New("incompatible MongoDB administration configuration")
		}
		store, err := openMongo(ctx)
		if err != nil {
			return nil, nil, err
		}
		verify := func(context.Context, string) bool { return false }
		if email != "" {
			verify = gateway.GoogleAdministrator(aud, email)
		}
		api, err := gateway.OpenAdmin(raw, version, revision, store, verify, console.Handler())
		if err != nil {
			_ = store.Close()
			return nil, nil, err
		}
		api.EnableHostedLogins(store)
		return api, func() { _ = store.Close() }, nil
	}
	if project == "" && db == "" && secret == "" && email == "" && aud == "" {
		api, err := gateway.Open(raw, version, revision)
		return api, func() {}, err
	}
	if project == "" || db == "" || secret == "" || email == "" || aud == "" {
		return nil, nil, errors.New("incomplete MLC_ADMIN configuration")
	}
	store, err := control.Open(ctx, project, db, secret)
	if err != nil {
		return nil, nil, errors.New("cannot initialize administration storage")
	}
	closeStore := func() { _ = store.Close() }
	api, err := gateway.OpenAdmin(raw, version, revision, store, gateway.GoogleAdministrator(aud, email), console.Handler())
	if err != nil {
		closeStore()
		return nil, nil, err
	}
	if os.Getenv("MLC_HOSTED_LOGIN_ENABLED") == "true" {
		api.EnableHostedLogins(store)
	}
	return api, closeStore, nil
}

// Secrets are mounted files so the process environment, image, and logs can
// be inspected without exposing database credentials or encryption keys.
func readSecretFile(env string) (string, error) {
	data, err := os.ReadFile(os.Getenv(env))
	if err != nil || len(data) > 64<<10 {
		return "", errors.New("cannot read " + env)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", errors.New("empty " + env)
	}
	return value, nil
}

func openMongo(ctx context.Context) (*control.MongoStore, error) {
	uri, err := readSecretFile("MLC_CONTROL_MONGO_URI_FILE")
	if err != nil {
		return nil, err
	}
	raw, err := readSecretFile("MLC_CONTROL_MONGO_KEY_FILE")
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.Strict().DecodeString(raw)
	if err != nil {
		return nil, errors.New("invalid control database encryption key")
	}
	return control.OpenMongo(ctx, uri, os.Getenv("MLC_CONTROL_MONGO_DATABASE"), key)
}

func initMongoOwner(ctx context.Context) error {
	store, err := openMongo(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	email, err := readSecretFile("MLC_OWNER_EMAIL_FILE")
	if err != nil {
		return err
	}
	password, err := readSecretFile("MLC_OWNER_PASSWORD_FILE")
	if err != nil {
		return err
	}
	a, err := control.NewAccountWithPassword(email, control.RoleOwner, true, password)
	if err != nil {
		return errors.New("invalid initial owner credentials")
	}
	err = store.BootstrapAccount(ctx, a)
	if errors.Is(err, control.ErrConflict) {
		found, readErr := store.Account(ctx, a.ID)
		if readErr != nil || !found.Primary {
			return errors.New("another primary owner exists")
		}
	} else if err != nil {
		return errors.New("cannot initialize primary owner")
	}
	if os.Getenv("MLC_SAMPLE_REPORTER_URI_FILE") != "" {
		uri, err := readSecretFile("MLC_SAMPLE_REPORTER_URI_FILE")
		if err != nil {
			return err
		}
		_, err = store.SaveConnection(ctx, "sample", map[string][]string{"analytics": {"orders"}}, uri, "")
		if err != nil && !errors.Is(err, control.ErrConflict) {
			return errors.New("cannot initialize sample connection")
		}
	}
	return nil
}
