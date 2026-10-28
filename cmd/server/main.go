// go-immich server entry point (Phase 8 unification).
//
// main() is intentionally thin: all wiring lives in internal/app so the same
// composition can be reused by tests or embedded tooling. Boot order:
//
//  1. load config from environment
//  2. app.New -> open SQLite (WAL), bootstrap schema, seed admin, build router
//  3. app.Run -> start compression worker + HTTP server, serve until
//     SIGINT/SIGTERM, then drain HTTP, stop worker, flush logs, close DB.
package main

import (
	"context"
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
	cfg := config.Load()
	if version != "" && version != config.ServerVersion {
		log.Printf("main: build-time version override active: %s", version)
	}

	// Build phase gets a plain context; Run installs the signal-bound one.
	a, err := app.New(context.Background(), cfg)
	if err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
	if err := a.Run(); err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
}
