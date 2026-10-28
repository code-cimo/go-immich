// go-immich server entry point (Phase 8 unification).
//
// main() is intentionally thin: all wiring lives in internal/app so the same
// composition can be reused by tests or embedded tooling. Boot order:
//
//  1. load config from environment
//  2. app.New -> open SQLite (WAL), bootstrap schema, seed admin, build router
//  3. app.Run -> start compression worker + HTTP server, serve until
//     SIGINT/SIGTERM, then drain HTTP, stop worker, flush logs, close DB.
//
// Flag --init-only performs steps 1-2 only and exits 0. This is what the TUI
// installer calls to provision the database/admin account without binding a
// port, guaranteeing schema parity between installer and server.
package main

import (
	"context"
	"flag"
	"log"
	"os"

	"go-immich/config"
	"go-immich/internal/app"
)

// version is overridable at link time:
//
//	go build -ldflags "-X main.version=v1.100.0"
var version = config.ServerVersion

func main() {
	initOnly := flag.Bool("init-only", false,
		"bootstrap storage directories, SQLite schema and the admin account, then exit")
	flag.Parse()

	cfg := config.Load()
	if version != "" && version != config.ServerVersion {
		log.Printf("main: build-time version override active: %s", version)
	}

	a, err := app.New(context.Background(), cfg)
	if err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
	if *initOnly {
		// Everything above already ran: dirs created, schema migrated, admin
		// seeded. Close cleanly (triggers the WAL checkpoint) and report.
		if err := a.CloseInit(); err != nil {
			log.Printf("fatal: init-only close: %v", err)
			os.Exit(1)
		}
		log.Printf("init-only: database ready at %s (admin=%s)", cfg.DBPath, cfg.AdminEmail)
		return
	}
	if err := a.Run(); err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
}
