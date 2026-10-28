// immich-go-installer - interactive TUI installer for the go-immich server.
//
// Single static binary (CGO_ENABLED=0) shipped for linux/amd64 and
// linux/arm64. It expects the matching server binaries to sit next to it:
//
//	./immich-go-installer   ./immich-go   [./immich-migrate]
//
// Modes:
//
//	(default)                 full Bubble Tea wizard with customisable setup
//	--unattended             no TTY required; env/flags provide the answers
//	--export-only            re-run diagnostics & emit a fresh debug log only
//
// Common to every mode: exactly ONE debug log file is exported
// (<data>/logs/immich-go-install-<timestamp>.log) containing host facts,
// resolved configuration (secrets masked) and a timestamped trace of every
// step - hand that single file to the maintainers when something breaks.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"go-immich/internal/installer"
)

var version = "1.100.0"

func main() {
	var (
		unattended = flag.Bool("unattended", false, "run without TUI using environment/defaults")
		exportOnly = flag.Bool("export-only", false, "only collect diagnostics into a debug log")
		dataDir    = flag.String("data-dir", "", "override data directory (also $IMMICH_DATA_DIR)")
		instDir    = flag.String("install-dir", "", "override install directory (also $IMMICH_INSTALL_DIR)")
		port       = flag.Int("port", 0, "override HTTP port (also $IMMICH_PORT)")
		email      = flag.String("admin-email", "", "admin e-mail ($IMMICH_ADMIN_EMAIL)")
		password   = flag.String("admin-password", "", "admin password ($IMMICH_ADMIN_PASSWORD)")
		noService  = flag.Bool("no-service", false, "skip systemd installation")
		log        = flag.String("log-file", "", "explicit path for the exported debug log")
	)
	flag.Parse()

	cfg := installer.DefaultCfg()
	applyOverrides(cfg, dataDir, instDir, port, email, password, noService)

	if *exportOnly {
		if err := runExportOnly(cfg, *log); err != nil {
			fmt.Fprintln(os.Stderr, "installer:", err)
			os.Exit(1)
		}
		return
	}

	if *unattended || os.Getenv("TERM") == "" || !isTTY() {
		if err := runUnattended(cfg, *log); err != nil {
			fmt.Fprintln(os.Stderr, "installer:", err)
			os.Exit(1)
		}
		return
	}

	// ---- interactive TUI -----------------------------------------------------
	m := installer.NewModel(cfg, version)
	p, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "installer: tui error:", err)
		os.Exit(1)
	}
	if fm, ok := p.(*installer.Model); ok && fm.FinalError() != nil {
		os.Exit(1)
	}
}

// applyOverrides merges CLI flags > environment > architecture defaults.
func applyOverrides(cfg *installer.InstallCfg, dataDir, instDir *string, port *int, email, password string, noService *bool) {
	if v := os.Getenv("IMMICH_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("IMMICH_INSTALL_DIR"); v != "" {
		cfg.InstallDir = v
	}
	if v := os.Getenv("IMMICH_PORT"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			cfg.Port = n
		}
	}
	if v := os.Getenv("IMMICH_ADMIN_EMAIL"); v != "" {
		cfg.AdminEmail = v
	}
	if v := os.Getenv("IMMICH_ADMIN_PASSWORD"); v != "" {
		cfg.AdminPassword = v
	}
	if v := os.Getenv("IMMICH_JWT_SECRET"); v != "" {
		cfg.JWTSecret = v
	}
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}
	if *instDir != "" {
		cfg.InstallDir = *instDir
	}
	if *port != 0 {
		cfg.Port = *port
	}
	if email != "" {
		cfg.AdminEmail = email
	}
	if password != "" {
		cfg.AdminPassword = password
	}
	if *noService {
		cfg.RunAsService = false
	}
}

// runUnattended executes the same engine as the TUI but streams progress to
// stdout - used by CI, Ansible or headless SSH sessions.
func runUnattended(cfg *installer.InstallCfg, logPath string) error {
	dlog, err := openDebugLog(cfg, logPath)
	if err != nil {
		return err
	}
	dlog.WriteHeader(cfg, version)
	eng := installer.NewEngine(cfg, dlog, func(ev installer.ProgressEvent) {
		fmt.Printf("[%s] %s %s\n", ev.Status, ev.Step, ev.Detail)
	})
	if err := eng.Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "debug log exported to: %s\n", dlog.Path())
		return err
	}
	fmt.Printf("done. debug log exported to: %s\n", dlog.Path())
	return nil
}

// runExportOnly performs zero mutations: fingerprint + config snapshot only.
func runExportOnly(cfg *installer.InstallCfg, logPath string) error {
	dlog, err := openDebugLog(cfg, logPath)
	if err != nil {
		return err
	}
	dlog.Section("diagnostics-only export")
	dlog.WriteHeader(cfg, version)
	dlog.WriteFooter(true)
	fmt.Println("diagnostics exported to:", dlog.Path())
	return nil
}

func openDebugLog(cfg *installer.InstallCfg, explicit string) (*installer.DebugLog, error) {
	if explicit != "" {
		dir := filepath.Dir(explicit)
		dl, err := installer.NewDebugLogAt(dir, filepath.Base(explicit))
		if err != nil {
			return nil, err
		}
		return dl, nil
	}
	dir := filepath.Join(cfg.DataDir, "logs")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		dir = os.TempDir() // unprivileged dev fallback
	}
	return installer.NewDebugLog(dir)
}

func isTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
