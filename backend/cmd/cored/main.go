// Command cored is the modular-monolith backend for the keystroke-compatible
// Tally competitor. It exposes two subcommands:
//
//	cored migrate   apply embedded SQL migrations as the ADMIN DSN
//	cored serve     run the chi HTTP server as the APP DSN
//
// Connection strings come from the environment:
//
//	ADMIN_DSN  used by `migrate` (default points at the local dev Postgres)
//	APP_DSN    used by `serve`   (least-privilege tally_app role)
//	PORT       HTTP listen port for `serve` (default 8080)
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/server"
	"github.com/khata/cored/migrations"
)

const (
	defaultAdminDSN = "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable"
	defaultAppDSN   = "postgres://tally_app:tally_app@localhost:5433/tally?sslmode=disable"
	defaultPort     = "8080"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	ctx := context.Background()

	var err error
	switch cmd {
	case "migrate":
		err = runMigrate(ctx)
	case "serve":
		err = runServe(ctx)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.Fatalf("cored %s: %v", cmd, err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: cored <migrate|serve>")
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func runMigrate(ctx context.Context) error {
	dsn := env("ADMIN_DSN", defaultAdminDSN)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect admin: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	if err := migrations.Apply(ctx, conn); err != nil {
		return err
	}
	names, _ := migrations.Names()
	log.Printf("migrations applied (%d files)", len(names))
	return nil
}

func runServe(ctx context.Context) error {
	dsn := env("APP_DSN", defaultAppDSN)
	port := env("PORT", defaultPort)

	srv, err := server.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer srv.Close()

	// Drain the notification outbox in the background (item C). Invites are
	// enqueued transactionally by the admin API; this is what actually delivers
	// them. Errors are logged inside Run, never fatal — a broken transport must
	// not take the server down.
	notifyCtx, stopNotify := context.WithCancel(ctx)
	defer stopNotify()
	go srv.RunNotifications(notifyCtx, 15*time.Second)

	addr := ":" + port
	log.Printf("cored serving on %s", addr)
	if err := srv.ListenAndServe(addr); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
