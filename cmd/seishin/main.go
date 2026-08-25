// Command seishin is the single entrypoint for every Seishin control-plane
// role.
//
// `seishin serve` runs every implemented service in one process (the
// self-hosting default). `seishin serve --role=<name>` runs a single
// service, talking to the others over gRPC, for distributed deployments.
// Composition is a wiring decision, not a separate code path — see
// docs/architecture.md section 5.1. Only the "api" role (identity, session,
// directory over HTTP) is implemented so far; every other role is a stub.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/seishinapp/seishin/gateway/api"
	"github.com/seishinapp/seishin/internal/core/directory"
	"github.com/seishinapp/seishin/internal/core/session"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "seishin:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] != "serve" {
		return errors.New("usage: seishin serve [--role=<name>]")
	}

	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	role := fs.String("role", "", "service to run alone (default: every implemented service)")
	addr := fs.String("addr", ":7700", "address the API gateway listens on")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	switch *role {
	case "", "api", "server":
		return serveAPI(*addr)
	default:
		return fmt.Errorf("role %q is not implemented yet", *role)
	}
}

func serveAPI(addr string) error {
	sessionSvc := session.NewService()
	directorySvc := directory.NewSeededService()
	handler := api.NewHandler(sessionSvc, directorySvc)

	srv := &http.Server{Addr: addr, Handler: handler}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErrCh := make(chan error, 1)
	go func() {
		slog.Info("api gateway listening", "addr", addr)
		serveErrCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErrCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		slog.Info("shutting down")
		return srv.Shutdown(context.Background())
	}
}
