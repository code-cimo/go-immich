// Package app is the Phase 8 orchestrator: it wires every dependency of the
// monolithic binary in one place and owns the graceful startup/shutdown
// sequence required on a headless SBC.
//
// Dependency graph (manual injection, no DI framework):
//
//	Config -> Database(*sql.DB) -> Repositories -> Services -> Middleware
//	                         \--> Handlers --> gin.Engine      \--> Worker
//
// Shutdown ordering matters for SQLite/WAL: HTTP drains first so no new writes
// arrive, the worker gets a short grace window to finish its current item, the
// async logger is flushed, and only then is the DB handle closed (the last
// connection close triggers the WAL checkpoint).
package app

import (
	"context"
	"errors"
	"fmt"
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

// BuildTimeout bounds schema bootstrap at start-up. Migrations here are
// idempotent CREATE IF NOT EXISTS statements and complete in milliseconds; a
// locked or corrupt DB must fail fast instead of hanging a service restart loop.
const BuildTimeout = 30 * time.Second

// workerDrainGrace is how long we wait for the background worker to finish its
// current item after context cancellation before closing the database anyway.
const workerDrainGrace = 5 * time.Second

// Repos groups the data-access layer.
type Repos struct {
	Users  *repository.UserRepo
	Assets *repository.AssetRepo
	Albums *repository.AlbumRepo
}

// Services groups business logic.
type Services struct {
	Auth *services.AuthService
	Info *services.ServerInfoService
}

// App is the composed root object. New() builds the graph, Run() serves.
type App struct {
	cfg    *config.Config
	sqlDB  interface{ Close() error } // concrete *sql.DB, kept narrow for tests
	engine *gin.Engine
	logger *applog.AsyncLogger
	worker *worker.CompressWorker

	repos    *Repos
	services *Services
}

// New constructs the full object graph but does not start any goroutine yet.
func New(ctx context.Context, cfg *config.Config) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("app: config: %w", err)
	}

	// ---- database -------------------------------------------------------------
	db, err := database.Open(ctx, cfg.DBPath, cfg.MaxConns)
	if err != nil {
		return nil, fmt.Errorf("app: open db: %w", err)
	}
	bootCtx, cancel := context.WithTimeout(ctx, BuildTimeout)
	defer cancel()
	if err := database.InitSchema(bootCtx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("app: schema: %w", err)
	}
	if err := database.EnsureStorageDirs(cfg.PhotosDir, cfg.ThumbnailsDir, cfg.EncodedDir); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("app: storage dirs: %w", err)
	}

	// ---- repositories -----------------------------------------------------------
	repos := &Repos{
		Users:  repository.NewUserRepo(db),
		Assets: repository.NewAssetRepo(db),
		Albums: repository.NewAlbumRepo(db),
	}

	// ---- services ----------------------------------------------------------------
	authSvc := services.NewAuthService(repos.Users, cfg)
	if err := authSvc.EnsureAdmin(bootCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("app: seed admin: %w", err)
	}
	infoSvc, err := services.NewServerInfoService(cfg)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("app: server info: %w", err)
	}

	// ---- middleware + router -------------------------------------------------------
	asyncLog := applog.Default()
	authMW := middleware.NewAuthMiddleware(cfg.JWTSecret, repos.Users)

	mode := gin.ReleaseMode
	if os.Getenv("IMMICH_DEBUG") == "1" {
		mode = gin.DebugMode
	}
	engine := handlers.BuildRouter(mode, &handlers.Deps{
		AuthMW:     authMW,
		Log:        asyncLog,
		Health:     handlers.NewHealthHandler(),
		ServerInfo: handlers.NewServerInfoHandler(infoSvc),
		Users:      handlers.NewUserHandler(repos.Users, authSvc),
		Auth:       handlers.NewAuthHandler(authSvc),
		Upload:     handlers.NewUploadHandler(cfg, repos.Assets),
		Assets:     handlers.NewAssetHandler(repos.Assets),
		Files:      handlers.NewFileHandler(cfg, repos.Assets),
		Albums:     handlers.NewAlbumHandler(repos.Albums, repos.Assets),
	})

	return &App{
		cfg:      cfg,
		sqlDB:    db,
		engine:   engine,
		logger:   asyncLog,
		worker:   worker.NewCompressWorker(cfg, repos.Assets),
		repos:    repos,
		services: &Services{Auth: authSvc, Info: infoSvc},
	}, nil
}

// Run executes the lifecycle: signal handling, worker start, HTTP serve, then
// ordered shutdown. It returns a non-nil error only when the process should
// exit with a failure status.
func (a *App) Run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ---- background worker (Phase 4), tied to the root context -----------------
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		if err := a.worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("app: worker exited with error: %v", err)
		}
	}()

	// ---- HTTP server ---------------------------------------------------------------
	srv := &http.Server{
		Addr:         a.cfg.Addr(),
		Handler:      a.engine,
		ReadTimeout:  a.cfg.ReadTimeout,
		WriteTimeout: a.cfg.WriteTimeout,
		IdleTimeout:  120 * time.Second,
	}
	serverErr := make(chan error, 1)
	go func() {
		log.Printf("go-immich %s listening on %s (db=%s)",
			config.ServerVersion, a.cfg.Addr(), a.cfg.DBPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case err := <-serverErr:
		if err != nil {
			a.shutdown(srv, workerDone)
			return fmt.Errorf("app: listen: %w", err)
		}
	case <-ctx.Done():
		log.Printf("app: shutdown signal received (budget %s)", a.cfg.ShutdownTimeout)
	}

	a.shutdown(srv, workerDone)
	return nil
}

// CloseInit tears down a build-only App (used by `--init-only`): flushes the
// async logger and closes the DB handle, which checkpoints the WAL. It must
// not be called after Run().
func (a *App) CloseInit() error {
	a.logger.Close()
	if a.sqlDB != nil {
		return a.sqlDB.Close()
	}
	return nil
}

// shutdown performs the ordered teardown described in the package comment.
func (a *App) shutdown(srv *http.Server, workerDone chan struct{}) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("app: http drain: %v", err)
	}

	select {
	case <-workerDone:
		log.Printf("app: worker drained")
	case <-time.After(workerDrainGrace):
		log.Printf("app: worker did not finish within %s - forcing ahead", workerDrainGrace)
	}

	a.logger.Close() // flush queued lines before exit

	if a.sqlDB != nil {
		if err := a.sqlDB.Close(); err != nil {
			log.Printf("app: db close: %v", err)
		} else {
			log.Printf("app: database closed (WAL checkpointed)")
		}
	}
	log.Printf("app: stopped cleanly")
}
