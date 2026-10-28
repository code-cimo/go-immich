// Package installer implements the interactive TUI installer for go-immich.
//
// It is a single static, pure-Go binary (CGO_ENABLED=0) that cross-runs on
// linux/amd64 (development machines) and linux/arm64 (ODROID-C2 production).
// The installer:
//
//  1. renders a Bubble Tea TUI where every deployment knob can be customised
//     (ports, data directory, admin credentials, worker/CPU/RAM tuning),
//  2. provisions directories + a validated SQLite database,
//  3. writes an env-file consumed by both systemd and Docker,
//  4. installs & enables the immich-go.service unit when running as root,
//  5. exports a single self-contained debug log file (immich-go-install.log)
//     that can be handed to the maintainers for remote troubleshooting.
package installer

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// InstallCfg is the fully-customisable setup collected through the TUI.
// Every field maps 1:1 onto an IMMICH_* environment variable understood by
// config.Load() in the server binary - the installer never needs to recompile
// anything, it only produces configuration.
type InstallCfg struct {
	// ---- target layout -----------------------------------------------------
	InstallDir   string // where binaries are copied (/opt/immich-go)
	DataDir      string // storage root; DB lives at <DataDir>/immich.db
	BinarySource string // path of the server binary bundled next to installer

	// ---- HTTP ----------------------------------------------------------------
	Port int // IMMICH_PORT (default 2283)

	// ---- identity / security ---------------------------------------------------
	AdminEmail    string // IMMICH_ADMIN_EMAIL
	AdminPassword string // IMMICH_ADMIN_PASSWORD
	AdminName     string // IMMICH_ADMIN_NAME (display name of the seeded admin)
	JWTSecret     string // IMMICH_JWT_SECRET (generated if empty)

	// ---- resource tuning (ODROID-C2 profile) ------------------------------------
	MaxConns        int // IMMICH_DB_MAX_CONNS
	WorkerIntervalS int // IMMICH_WORKER_INTERVAL (seconds)
	WorkerMaxCPU    int // IMMICH_WORKER_MAX_CPU (%)
	ResizeConc      int // IMMICH_RESIZE_CONCURRENCY
	MemLimitMB      int // IMMICH_MEMORY_LIMIT_MB
	ThumbSize       int // IMMICH_THUMBNAIL_SIZE

	// ---- behaviour toggles ------------------------------------------------------
	RunAsService bool // install + enable the systemd unit (requires root)
	StartNow     bool // systemctl start immediately after install
	OptimizeOS   bool // apply vm.swappiness etc. (arm64/SBC recommendation)

	// runtime facts detected at startup (not user-editable)
	GOARCH   string
	GOOS     string
	HasRoot  bool
	HasSysd  bool
}

// DefaultCfg returns sane defaults for the detected architecture: the SBC
// profile (arm64) is pre-tuned for a 2GB ODROID-C2, the amd64 profile assumes
// a beefier development box.
func DefaultCfg() *InstallCfg {
	c := &InstallCfg{
		InstallDir:      "/opt/immich-go",
		DataDir:         "/data",
		Port:            2283,
		AdminEmail:      "admin@example.com",
		AdminPassword:   "",
		AdminName:       "Administrator",
		MaxConns:        5,
		WorkerIntervalS: 15,
		WorkerMaxCPU:    20,
		ResizeConc:      2,
		MemLimitMB:      500,
		ThumbSize:       256,
		RunAsService:    true,
		StartNow:        true,
	}
	switch detectArch() {
	case "arm64":
		// Conservative profile for Amlogic S905 / 2GB RAM boards.
		c.OptimizeOS = true
	default:
		// x86_64 dev machine: leave OS tuning off, allow more parallelism.
		c.OptimizeOS = false
		c.ResizeConc = 4
		c.MemLimitMB = 1024
	}
	if src, err := os.Executable(); err == nil {
		c.BinarySource = filepath.Join(filepath.Dir(src), "immich-go")
	}
	return c
}

// detectArch reports the GOARCH of the running installer ("amd64"/"arm64").
func detectArch() string {
	if arch := os.Getenv("IMMICH_INSTALLER_ARCH_OVERRIDE"); arch != "" {
		return arch // test hook for simulating the other platform
	}
	return runtime.GOARCH
}

// detectOS reports the GOOS of the running installer.
func detectOS() string { return runtime.GOOS }

// Validate checks every user-supplied value before any mutation happens.
// Returns a list of human-readable problems (empty slice == valid).
func (c *InstallCfg) Validate() []string {
	var errs []string
	if !filepath.IsAbs(c.InstallDir) {
		errs = append(errs, "install dir must be an absolute path")
	}
	if !filepath.IsAbs(c.DataDir) {
		errs = append(errs, "data dir must be an absolute path")
	}
	if c.Port < 1 || c.Port > 65535 {
		errs = append(errs, "port must be between 1 and 65535")
	}
	if e := strings.TrimSpace(c.AdminEmail); e == "" || !strings.Contains(e, "@") {
		errs = append(errs, "admin email looks invalid (missing '@')")
	}
	if len(c.AdminPassword) < 8 {
		errs = append(errs, "admin password must be at least 8 characters")
	}
	if c.MaxConns < 2 || c.MaxConns > 50 {
		errs = append(errs, "db max connections should be 2..50")
	}
	if c.WorkerIntervalS < 1 || c.WorkerIntervalS > 3600 {
		errs = append(errs, "worker interval should be 1..3600 seconds")
	}
	if c.WorkerMaxCPU < 1 || c.WorkerMaxCPU > 100 {
		errs = append(errs, "worker CPU budget should be 1..100 %")
	}
	if c.ResizeConc < 1 || c.ResizeConc > 16 {
		errs = append(errs, "resize concurrency should be 1..16")
	}
	if c.MemLimitMB < 64 || c.MemLimitMB > 65536 {
		errs = append(errs, "memory limit should be 64..65536 MB")
	}
	if c.ThumbSize < 64 || c.ThumbSize > 1024 {
		errs = append(errs, "thumbnail size should be 64..1024 px")
	}
	if c.RunAsService && !c.HasRoot {
		errs = append(errs, "systemd service installation requires root (sudo)")
	}
	return errs
}

// PortOpen reports whether the configured port is already bound by something
// else - caught early so the user can change it inside the TUI instead of
// getting a confusing crash after "Installation complete".
func (c *InstallCfg) PortOpen() error {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(c.Port))
	if err != nil {
		return fmt.Errorf("port %d busy or not bindable: %w", c.Port, err)
	}
	_ = ln.Close()
	return nil
}

// GenerateJWT creates a fresh 256-bit hex secret if the user left the field
// blank. Called during execution, logged (masked) into the debug export.
func (c *InstallCfg) GenerateJWT() {
	if c.JWTSecret != "" {
		return
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failing is catastrophic anyway; fall back to a fixed
		// marker so the server's own boot-time generator takes over.
		c.JWTSecret = ""
		return
	}
	c.JWTSecret = hex.EncodeToString(buf)
}

// EnvFileContent renders the env file consumed by systemd (EnvironmentFile)
// and docker-compose (env_file). One IMMICH_* variable per line.
func (c *InstallCfg) EnvFileContent() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# go-immich generated by the TUI installer (%s/%s)\n", c.GOOS, c.GOARCH)
	fmt.Fprintf(&b, "# Do not edit by hand unless you know what you are doing.\n")
	fmt.Fprintf(&b, "IMMICH_DATA_DIR=%s\n", c.DataDir)
	fmt.Fprintf(&b, "IMMICH_DB_PATH=%s\n", filepath.Join(c.DataDir, "immich.db"))
	fmt.Fprintf(&b, "IMMICH_PHOTOS_DIR=%s\n", filepath.Join(c.DataDir, "photos"))
	fmt.Fprintf(&b, "IMMICH_THUMBNAILS_DIR=%s\n", filepath.Join(c.DataDir, "thumbnails"))
	fmt.Fprintf(&b, "IMMICH_ENCODED_DIR=%s\n", filepath.Join(c.DataDir, "encoded"))
	fmt.Fprintf(&b, "IMMICH_PORT=%d\n", c.Port)
	fmt.Fprintf(&b, "IMMICH_JWT_SECRET=%s\n", c.JWTSecret)
	fmt.Fprintf(&b, "IMMICH_ADMIN_EMAIL=%s\n", c.AdminEmail)
	fmt.Fprintf(&b, "IMMICH_ADMIN_PASSWORD=%s\n", c.AdminPassword)
	fmt.Fprintf(&b, "IMMICH_ADMIN_NAME=%s\n", c.AdminName)
	fmt.Fprintf(&b, "IMMICH_DB_MAX_CONNS=%d\n", c.MaxConns)
	fmt.Fprintf(&b, "IMMICH_WORKER_INTERVAL=%ds\n", c.WorkerIntervalS)
	fmt.Fprintf(&b, "IMMICH_WORKER_MAX_CPU=%d\n", c.WorkerMaxCPU)
	fmt.Fprintf(&b, "IMMICH_RESIZE_CONCURRENCY=%d\n", c.ResizeConc)
	fmt.Fprintf(&b, "IMMICH_MEMORY_LIMIT_MB=%d\n", c.MemLimitMB)
	fmt.Fprintf(&b, "IMMICH_THUMBNAIL_SIZE=%d\n", c.ThumbSize)
	return b.String()
}
