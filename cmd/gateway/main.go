package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Hexcss/mongo-looker-connector/internal/console"
	"github.com/Hexcss/mongo-looker-connector/internal/control"
	"github.com/Hexcss/mongo-looker-connector/internal/gateway"
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
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return errors.New("PORT must be a valid TCP port")
	}
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
	server := &http.Server{
		Addr: ":" + port, Handler: api,
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
	return api, closeStore, nil
}
