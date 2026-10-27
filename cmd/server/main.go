// go-immich server entry point.
//
// Boot sequence:
//  1. load config from environment
//  2. open SQLite (WAL) + bootstrap schema + storage dirs
//  3. seed the admin account if the users table is empty
//  4. build the Gin router with all Immich-compatible routes
//  5. start the low-priority compression worker goroutine
//  6. serve HTTP until SIGINT/SIGTERM, then shut down gracefully
package main

import (
	"context"
	"errors"
	"log"
"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"go-immich/config"
	"go-immich/internal/database"
	"go-immich/internal/handlers"
	"go-immich/internal/middleware"
	"go-immich/internal/repository"
	"go-immich/internal/services"
	"go-immich/internal/worker"
	applog "go-immich/pkg/logger"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("fatal: %v", err)
	}
}

func run() error {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		return err
	}

	// Root context cancelled on SIGINT/SIGTERM - everything downstream
	// (HTTP drain, worker, async logger) observes it.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(ctx, cfg.DBPath, cfg.MaxConns)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	if err := database.InitSchema(ctx, db); err != nil {
		return err
	}
	if err := database.EnsureStorageDirs(cfg.PhotosDir, cfg.ThumbnailsDir, cfg.EncodedDir); err != nil {
		return err
	}

	// repositories / services -------------------------------------------------
	userRepo := repository.NewUserRepo(db)
	assetRepo := repository.NewAssetRepo(db)
	albumRepo := repository.NewAlbumRepo(db)

	authSvc := services.NewAuthService(userRepo, cfg)
	if err := authSvc.EnsureAdmin(ctx); err != nil {
		return err
	}
	infoSvc, err := services.NewServerInfoService(cfg)
	if err != nil {
		return err
	}

	// middleware ---------------------------------------------------------------
	asyncLog := applog.Default()
	authMW := middleware.NewAuthMiddleware(cfg.JWTSecret, userRepo)

	// router -------------------------------------------------------------------
	mode := gin.ReleaseMode
	if os.Getenv("IMMICH_DEBUG") == "1" {
		mode = gin.DebugMode
	}
	engine := handlers.BuildRouter(mode, &handlers.Deps{
		AuthMW:     authMW,
		Log:        asyncLog,
		Health:     handlers.NewHealthHandler(),
		ServerInfo: handlers.NewServerInfoHandler(infoSvc),
		Users:      handlers.NewUserHandler(userRepo, authSvc),
		Auth:       handlers.NewAuthHandler(authSvc),
		Upload:     handlers.NewUploadHandler(cfg, assetRepo),
		Assets:     handlers.NewAssetHandler(assetRepo),
		Files:      handlers.NewFileHandler(cfg, assetRepo),
		Albums:     handlers.NewAlbumHandler(albumRepo, assetRepo),
	})

	srv := &http.Server{
		Addr:         cfg.Addr(),
		Handler:      engine,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  120 * time.Second,
	}

	// background worker ---------------------------------------------------------
	compWorker := worker.NewCompressWorker(cfg, assetRepo)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		if err := compWorker.Run(ctx); err != nil {
			log.Printf("worker exited with error: %v", err)
		}
	}()

	// serve ----------------------------------------------------------------------
	serverErr := make(chan error, 1)
	go func() {
		log.Printf("go-immich %s listening on %s (db=%s)", config.ServerVersion, cfg.Addr(), cfg.DBPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case err := <-serverErr:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		log.Printf("shutdown signal received - draining for %s", cfg.ShutdownTimeout)
	}

	// graceful shutdown: stop accepting, let in-flight uploads finish briefly.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
	<-workerDone   // worker observes ctx cancellation
	asyncLog.Close() // flush queued log lines before exit
	log.Printf("go-immich stopped cleanly")
	return nil
}
