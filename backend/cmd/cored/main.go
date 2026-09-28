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
//
// Agent-plane telemetry (PLAN-AGENTIC W0) is OFF unless it is configured:
//
//	OTEL_EXPORTER_OTLP_TRACES_ENDPOINT  the collector's /v1/traces URL. UNSET
//	                                    means tracing is off, and the process
//	                                    SAYS SO on one line at boot.
//	OTEL_EXPORTER_OTLP_HEADERS          "k=v,k=v" (a Langfuse basic-auth header)
//	OTEL_SERVICE_NAME                   default "cored"
//	TRAJECTORY_SAMPLE_RATIO             head-based, default 1.0
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/server"
	"github.com/khata/cored/internal/trajectory"
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
		// ⛔ THE SIGNAL HANDLER IS WHAT MAKES EVERY defer IN runServe REAL. A
		// container restart, a `kubectl rollout`, a Ctrl+C and a systemd stop all
		// arrive as SIGTERM or SIGINT; with no handler the Go runtime's default
		// disposition kills the process where it stands, INSIDE
		// ListenAndServe — so the deferred trajectory Flush/Close and the
		// exported/failed/dropped accounting line never ran, and the exporter's
		// last batch (up to 512 spans, or 5 seconds of them) was dropped on every
		// single deploy. That was measured against the real binary, not assumed.
		//
		// ⚠️ stop() IS DEFERRED SO THE SECOND SIGNAL IS FATAL AGAIN. NotifyContext
		// only intercepts while the notification is registered; if a drain hangs,
		// an operator's second Ctrl+C must still be able to end the process rather
		// than being swallowed by the same handler.
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
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
	// ⛔ LOUD, NOT SILENT (0098): a database without pgvector migrates, and
	// retrieval runs lexical-only on it. That is a legitimate state for a
	// dev box, and a WRONG one for production — so the operator reads which
	// one they are in, with the fix, every time they migrate. A Postgres
	// WARNING raised inside the migration is not enough: pgx does not print
	// notices.
	var enabled bool
	var detail string
	if err := conn.QueryRow(ctx, `SELECT enabled, detail FROM emb_vector_status()`).Scan(&enabled, &detail); err != nil {
		return fmt.Errorf("read embeddings status: %w", err)
	}
	if enabled {
		log.Printf("embeddings: %s", detail)
	} else {
		log.Printf("embeddings: DISABLED — %s (lexical search only; see docs/design/d122-w3-data-understanding.md, Wave 2)", strings.TrimPrefix(detail, "disabled: "))
	}
	return nil
}

func runServe(ctx context.Context) error {
	dsn := env("APP_DSN", defaultAppDSN)
	port := env("PORT", defaultPort)

	// TRAJECTORY EXPORTER (W0). Constructed BEFORE the server, because the
	// server's own boot does work worth tracing and because a telemetry
	// misconfiguration should be found before a connection pool is opened.
	//
	// THE THREE OUTCOMES, AND WHY NONE OF THEM IS QUIET:
	//
	//   endpoint unset      -> (nil, nil). Tracing stays at trajectory.Nop(),
	//                          which is the zero-allocation path every process
	//                          runs today, and NewOTLPRecorder itself prints the
	//                          one line saying so — this function deliberately
	//                          does not print a second.
	//   endpoint malformed  -> an error, and cored REFUSES TO SERVE. That is the
	//                          fail-closed choice on purpose: a `grpc://`
	//                          endpoint or a mistyped OTEL_EXPORTER_OTLP_HEADERS
	//                          would otherwise produce a server that looks
	//                          instrumented and exports nothing, and an
	//                          observability gap is only ever discovered during
	//                          the incident it was supposed to explain.
	//   endpoint good       -> installed as THE process recorder.
	//
	// ⛔ SetRecorder IS CALLED ONLY WHEN rec IS NON-NIL. Handing it a typed-nil
	// *OTLPRecorder would install a non-nil Recorder interface wrapping nothing,
	// which defeats the nop fast path for every span in the process — the
	// classic typed-nil-in-an-interface bug, in the one place where it would cost
	// an allocation on every model call in a deployment that asked for no
	// tracing at all.
	rec, err := trajectory.NewOTLPRecorder(ctx)
	if err != nil {
		return fmt.Errorf("trajectory exporter: %w — refusing to serve with telemetry that would silently export nothing", err)
	}
	if rec != nil {
		trajectory.SetRecorder(rec)
		// ⚠️ FLUSH THEN CLOSE, AND BOTH ON THE WAY OUT. The exporter batches
		// (512 spans or 5s), so without the flush the last batch of a graceful
		// shutdown — which is exactly the batch containing whatever the operator
		// was investigating when they restarted the process — is dropped. The
		// flush gets its own deadline rather than the server's, because ctx is
		// already cancelled by the time a shutdown reaches here and a flush on a
		// cancelled context is a flush that does nothing.
		defer func() {
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := rec.Flush(flushCtx); err != nil {
				log.Printf("cored: trajectory flush on shutdown: %v", err)
			}
			if err := rec.Close(); err != nil {
				log.Printf("cored: trajectory close: %v", err)
			}
			// The counters are the honest accounting of what this process
			// actually got out of the door. Dropped is not an error condition —
			// Record never blocks, by design, so a wedged collector costs spans
			// and never latency — but it must be SAID, or "we have tracing" and
			// "we have the traces" become the same claim.
			log.Printf("cored: trajectory spans exported=%d failed=%d dropped=%d",
				rec.Exported(), rec.Failed(), rec.Dropped())
		}()
	}

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

	// The agent poller (W2, D121 / D120 F5): re-invokes due agent runs —
	// parked approvals, transient retries — and anchors the intent log daily.
	// Safe on every replica at once: each run is advanced under its own
	// advisory lock, so a second poller on the same run returns immediately.
	go srv.RunAgentPoller(notifyCtx, 2*time.Second)

	addr := ":" + port
	log.Printf("cored serving on %s", addr)
	if err := srv.ListenAndServe(ctx, addr); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
