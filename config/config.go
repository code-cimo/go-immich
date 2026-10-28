// Package config centralises all runtime configuration for the go-immich
// server. Every value can be overridden through environment variables so the
// same binary can run on an ODROID-C2 (DietPi), in Docker, or during local
// development without recompilation.
package config

import (
	"log"
	"os"
	"strconv"
	"time"
)

// ServerVersion is reported through /api/server-info/version and
// /api/server-info statistics. It intentionally matches an Immich mobile
// client-compatible release line.
const ServerVersion = "v1.100.0"

// Config holds every tunable knob of the server.
type Config struct {
	// HTTP
	Port            int           // listen port (default 2283, the Immich default)
	ReadTimeout     time.Duration // slow-device friendly read timeout
	WriteTimeout    time.Duration // large video downloads need a generous window
	ShutdownTimeout time.Duration // graceful-shutdown budget

	// Storage layout
	DBPath        string // SQLite database file
	PhotosDir     string // original uploads   /data/photos
	ThumbnailsDir string // generated thumbs    /data/thumbnails
	EncodedDir    string // transcoded videos   /data/encoded

	// Security
	JWTSecret       string
	TokenValidity   time.Duration // 30 days to match Immich mobile expectations
	AdminEmail        string // IMMICH_ADMIN_EMAIL (seeded only when users table is empty)
	AdminPassword     string // IMMICH_ADMIN_PASSWORD (seeded only when users table is empty)
	AdminName         string // IMMICH_ADMIN_NAME (display name of the seeded admin)
	AllowInsecureAuth bool   // accept ?key= query param (legacy clients)

	// Worker tuning (ODROID-C2: Amlogic S905 quad-core, 2GB RAM)
	WorkerInterval     time.Duration // base tick of the compression worker
	WorkerMaxCPUPct    int           // soft CPU budget (%) for background work
	ResizeConcurrency  int           // max simultaneous image resizes
	ResizeMaxWidth     int           // downscale originals wider than this
	ResizeQuality      int           // JPEG quality (0-100) for recompression
	ThumbnailSize      int           // gallery thumbnail edge length (px)
	MemoryLimitBytes   uint64        // soft memory ceiling; workers back off above it

	// Database
	MaxConns int // SQLite connection pool size (WAL readers + 1 writer)
}

// Load reads the environment and returns a fully-populated Config with safe
// defaults for resource-constrained ARM64 boards.
func Load() *Config {
	c := &Config{
		Port:              envInt("IMMICH_PORT", 2283),
		ReadTimeout:       envDuration("IMMICH_READ_TIMEOUT", 300*time.Second),
		WriteTimeout:      envDuration("IMMICH_WRITE_TIMEOUT", 600*time.Second),
		ShutdownTimeout:   envDuration("IMMICH_SHUTDOWN_TIMEOUT", 15*time.Second),
		DBPath:            envStr("IMMICH_DB_PATH", "/data/immich.db"),
		PhotosDir:         envStr("IMMICH_PHOTOS_DIR", "/data/photos"),
		ThumbnailsDir:     envStr("IMMICH_THUMBNAILS_DIR", "/data/thumbnails"),
		EncodedDir:        envStr("IMMICH_ENCODED_DIR", "/data/encoded"),
		JWTSecret:         envStr("IMMICH_JWT_SECRET", ""),
		TokenValidity:     envDuration("IMMICH_TOKEN_VALIDITY", 30*24*time.Hour),
		AdminEmail:        envStr("IMMICH_ADMIN_EMAIL", "admin@example.com"),
		AdminPassword:     envStr("IMMICH_ADMIN_PASSWORD", "admin123"),
		AdminName:         envStr("IMMICH_ADMIN_NAME", "Administrator"),
		WorkerInterval:    envDuration("IMMICH_WORKER_INTERVAL", 15*time.Second),
		WorkerMaxCPUPct:   envInt("IMMICH_WORKER_MAX_CPU", 20),
		ResizeConcurrency: envInt("IMMICH_RESIZE_CONCURRENCY", 2),
		ResizeMaxWidth:    envInt("IMMICH_RESIZE_MAX_WIDTH", 2048),
		ResizeQuality:     envInt("IMMICH_RESIZE_QUALITY", 80),
		ThumbnailSize:     envInt("IMMICH_THUMBNAIL_SIZE", 256),
		MemoryLimitBytes:  uint64(envInt("IMMICH_MEMORY_LIMIT_MB", 500)) * 1024 * 1024,
		MaxConns:          envInt("IMMICH_DB_MAX_CONNS", 5),
	}

	// A missing secret is a security hazard: generate one per boot and warn
	// loudly so operators pin it via the environment for stable sessions.
	if c.JWTSecret == "" {
		buf := make([]byte, 32)
		if _, err := randRead(buf); err != nil {
			log.Fatalf("config: cannot source entropy for JWT secret: %v", err)
		}
		c.JWTSecret = hexEncode(buf)
		log.Printf("config: IMMICH_JWT_SECRET not set - generated an ephemeral secret;"+
			" sessions will not survive restarts (%s)", c.DBPath)
	}
	return c
}

// Validate performs cheap sanity checks and fails fast on impossible values.
func (c *Config) Validate() error {
	if c.Port <= 0 || c.Port > 65535 {
		log.Printf("config: invalid port %d, falling back to 2283", c.Port)
		c.Port = 2283
	}
	if c.MaxConns < 2 {
		c.MaxConns = 2 // need at least 1 writer + 1 reader under WAL
	}
	if c.ResizeConcurrency < 1 {
		c.ResizeConcurrency = 1
	}
	if c.ThumbnailSize < 64 {
		c.ThumbnailSize = 64
	}
	return nil
}

// Addr returns the TCP listen address for the HTTP server.
func (c *Config) Addr() string { return ":" + strconv.Itoa(c.Port) }

// ---------- small env helpers (no third-party dependency) ----------

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		log.Printf("config: %s=%q is not an integer, using default %d", key, v, def)
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		log.Printf("config: %s=%q is not a duration (e.g. \"30s\"), using default %s", key, v, def)
	}
	return def
}
