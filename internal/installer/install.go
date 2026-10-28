// Execution engine of the installer. Each Step is one visible line in the
// TUI progress page AND one timestamped block in the exported debug log, so
// what the user sees and what the maintainers receive are always identical.
//
// Design rules:
//   - steps are idempotent (re-running the installer upgrades in place),
//   - nothing panics; every failure becomes a structured error that stops the
//     chain but still flushes the debug log,
//   - privileged actions (systemd, sysctl) are skipped with a warning when
//     not root instead of failing hard, so dev boxes can do a "files-only"
//     install.
package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Step is a single unit of installation work.
type Step struct {
	Name string
	Fn   func(ctx context.Context, cfg *InstallCfg, log *DebugLog) error
}

// Engine runs the plan and streams progress to both the debug log and an
// optional callback consumed by the TUI.
type Engine struct {
	cfg     *InstallCfg
	log     *DebugLog
	steps   []Step
	onEvent func(ProgressEvent)
}

// ProgressEvent is pushed to the TUI for each step transition.
type ProgressEvent struct {
	Step    string
	Status  string // "running" | "ok" | "fail" | "skip"
	Detail  string
	Index   int
	Total   int
	Elapsed time.Duration
}

// NewEngine assembles the canonical installation plan.
func NewEngine(cfg *InstallCfg, log *DebugLog, onEvent func(ProgressEvent)) *Engine {
	e := &Engine{cfg: cfg, log: log, onEvent: onEvent}
	e.steps = []Step{
		{"Preflight checks", stepPreflight},
		{"Create directory layout", stepDirs},
		{"Copy server binaries", stepBinaries},
		{"Write env file", stepEnvFile},
		{"Initialise SQLite database", stepDatabase},
		{"Apply OS tuning", stepOSTuning},
		{"Install systemd unit", stepSystemd},
		{"Start & verify service", stepStartVerify},
	}
	return e
}

func (e *Engine) emit(ev ProgressEvent) {
	if e.onEvent != nil {
		e.onEvent(ev)
	}
}

// Run executes every step in order. It returns the first fatal error; the
// debug log footer is written either way so evidence always exists.
func (e *Engine) Run(ctx context.Context) error {
	total := len(e.steps)
	ok := true
	for i, s := range e.steps {
		if ctx.Err() != nil {
			ok = false
			e.log.Line("cancelled before step %q: %v", s.Name, ctx.Err())
			break
		}
		e.emit(ProgressEvent{Step: s.Name, Status: "running", Index: i, Total: total})
		e.log.Line("STEP %d/%d START : %s", i+1, total, s.Name)
		start := time.Now()
		err := s.Fn(ctx, e.cfg, e.log)
		el := time.Since(start)
		switch {
		case err == nil:
			e.log.Line("STEP %d/%d OK    : %s (%s)", i+1, total, s.Name, el.Round(time.Millisecond))
			e.emit(ProgressEvent{Step: s.Name, Status: "ok", Index: i, Total: total, Elapsed: el})
		case errors.Is(err, errSkip):
			detail := strings.TrimPrefix(strings.TrimPrefix(err.Error(), errSkip.Error()), ": ")
			e.log.Line("STEP %d/%d SKIP  : %s (%s)", i+1, total, s.Name, detail)
			e.emit(ProgressEvent{Step: s.Name, Status: "skip", Detail: detail, Index: i, Total: total, Elapsed: el})
		default:
			ok = false
			e.log.Line("STEP %d/%d FAIL  : %s -> %v", i+1, total, s.Name, err)
			e.log.RecordFailure(s.Name)
			e.emit(ProgressEvent{Step: s.Name, Status: "fail", Detail: err.Error(), Index: i, Total: total, Elapsed: el})
		}
		if !ok {
			break
		}
	}
	e.log.WriteFooter(ok)
	if !ok {
		return fmt.Errorf("installation failed - see debug log at %s", e.log.Path())
	}
	return nil
}

// errSkip wraps "this step is intentionally not applicable here".
var errSkip = errors.New("skip")

// ---------- step implementations ----------

func stepPreflight(ctx context.Context, cfg *InstallCfg, log *DebugLog) error {
	if errs := cfg.Validate(); len(errs) > 0 {
		return fmt.Errorf("invalid setup: %s", strings.Join(errs, "; "))
	}
	if err := cfg.PortOpen(); err != nil {
		return err
	}
	// The bundled server binary must exist unless we are only doing a dry run.
	if _, err := os.Stat(cfg.BinarySource); err != nil {
		return fmt.Errorf("server binary not found next to installer (%s): %w", cfg.BinarySource, err)
	}
	log.Line("preflight : port %d free, binary %s present", cfg.Port, cfg.BinarySource)
	return nil
}

func stepDirs(ctx context.Context, cfg *InstallCfg, log *DebugLog) error {
	dirs := []string{
		cfg.InstallDir,
		cfg.DataDir,
		filepath.Join(cfg.DataDir, "photos"),
		filepath.Join(cfg.DataDir, "thumbnails"),
		filepath.Join(cfg.DataDir, "encoded"),
		filepath.Join(cfg.DataDir, "logs"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
		log.Line("dir ready : %s", d)
	}
	return nil
}

func stepBinaries(ctx context.Context, cfg *InstallCfg, log *DebugLog) error {
	targets := map[string]string{
		"immich-go":       cfg.BinarySource,
		"immich-migrate":  siblingBinary(cfg.BinarySource, "immich-migrate"),
	}
	for name, src := range targets {
		if src == "" {
			log.Line("binary    : %s not bundled, skipping", name)
			continue
		}
		if _, err := os.Stat(src); err != nil {
			if name != "immich-go" {
				continue // optional companion tools
			}
			return fmt.Errorf("copy %s: source missing: %w", name, err)
		}
		dst := filepath.Join(cfg.InstallDir, name)
		if err := copyFile32K(src, dst, 0o755); err != nil {
			return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
		}
		log.Line("binary    : installed %s (%s)", dst, humanSize(dst))
	}
	return nil
}

func stepEnvFile(ctx context.Context, cfg *InstallCfg, log *DebugLog) error {
	cfg.GenerateJWT()
	envPath := filepath.Join(cfg.InstallDir, "immich-go.env")
	content := cfg.EnvFileContent()
	// 0600: contains JWT + admin bootstrap password.
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write env file: %w", err)
	}
	_ = os.Chmod(envPath, 0o600)
	log.Line("env file  : %s written (0600, %d vars)", envPath, strings.Count(content, "\n"))
	return nil
}

func stepDatabase(ctx context.Context, cfg *InstallCfg, log *DebugLog) error {
	// Always bootstrap through the server binary ("--init-only"): it owns the
	// schema (CREATE ... IF NOT EXISTS + idempotent ALTER TABLE column
	// upgrades) and admin seeding. Skipping existing databases would leave
	// legacy schemas missing newer columns and crash the service on boot
	// with "SQL logic error: no such column".
	bin := filepath.Join(cfg.InstallDir, "immich-go")
	cmd := exec.CommandContext(ctx, bin, "--init-only")
	cmd.Env = append(os.Environ(), envPairs(cfg)...)
	out, err := cmd.CombinedOutput()
	log.Raw(indentBlock(string(out), "  db| "))
	if err != nil {
		return fmt.Errorf("database init: %w", err)
	}
	dbPath := filepath.Join(cfg.DataDir, "immich.db")
	if st, statErr := os.Stat(dbPath); statErr == nil && st.Size() > 0 {
		log.Line("database  : %s (%s) - schema verified/upgraded in place", dbPath, humanSize(dbPath))
	} else {
		log.Line("database  : %s initialised, admin seeded as %s", dbPath, cfg.AdminEmail)
	}
	return nil
}

func stepOSTuning(ctx context.Context, cfg *InstallCfg, log *DebugLog) error {
	if !cfg.OptimizeOS {
		return fmt.Errorf("%w: disabled by user", errSkip)
	}
	if !cfg.HasRoot {
		return fmt.Errorf("%w: requires root", errSkip)
	}
	sysctls := map[string]string{
		"vm.swappiness":          "10",
		"vm.vfs_cache_pressure":  "50",
		"vm.min_free_kbytes":     "65536",
	}
	conf := "/etc/sysctl.d/99-immich-go.conf"
	var b strings.Builder
	b.WriteString("# applied by go-immich installer - SBC-friendly memory pressure\n")
	var applied []string
	for k, v := range sysctls {
		fmt.Fprintf(&b, "%s = %s\n", k, v)
		if err := os.WriteFile("/proc/sys/"+strings.ReplaceAll(k, ".", "/"), []byte(v), 0); err != nil {
			log.Line("sysctl    : %s not applied here: %v", k, err)
			continue
		}
		applied = append(applied, k+"="+v)
	}
	if err := os.WriteFile(conf, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", conf, err)
	}
	log.Line("os tuning : persisted %s, live-applied: %s", conf, strings.Join(applied, ", "))
	return nil
}

func stepSystemd(ctx context.Context, cfg *InstallCfg, log *DebugLog) error {
	if !cfg.RunAsService {
		return fmt.Errorf("%w: user chose manual/docker mode", errSkip)
	}
	if !cfg.HasSysd {
		return fmt.Errorf("%w: no systemd on this host (container?), use docker-compose.dev.yml", errSkip)
	}
	if !cfg.HasRoot {
		return fmt.Errorf("%w: requires root", errSkip)
	}
	unit := renderUnit(cfg)
	unitPath := "/etc/systemd/system/immich-go.service"
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("write unit: %w", err)
	}
	log.Line("systemd   : wrote %s", unitPath)
	if out, err := run(ctx, "systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %v: %s", err, out)
	}
	if out, err := run(ctx, "systemctl", "enable", "immich-go"); err != nil {
		return fmt.Errorf("enable: %v: %s", err, out)
	}
	log.Line("systemd   : unit enabled for boot")
	return nil
}

func stepStartVerify(ctx context.Context, cfg *InstallCfg, log *DebugLog) error {
	if !cfg.RunAsService || !cfg.HasRoot || !cfg.HasSysd {
		// Files-only mode: run a short self-boot check directly against the
		// freshly created DB so we still verify the stack end-to-end.
		return smokeTestProcess(ctx, cfg, log)
	}
	if !cfg.StartNow {
		return fmt.Errorf("%w: start deferred by user", errSkip)
	}
	if out, err := run(ctx, "systemctl", "restart", "immich-go"); err != nil {
		return fmt.Errorf("restart: %v: %s", err, out)
	}
	// Give systemd a moment then probe /api/server-info/version over TCP.
	deadline := time.Now().Add(20 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := httpProbe(cfg); err == nil {
			log.Line("verify    : GET /api/server-info/version answered on :%d", cfg.Port)
			journal, _ := run(ctx, "journalctl", "-u", "immich-go", "-n", "40", "--no-pager")
			log.Raw(indentBlock(journal, "  journal| "))
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(500 * time.Millisecond)
	}
	status, _ := run(ctx, "systemctl", "status", "immich-go", "--no-pager", "-l")
	log.Raw(indentBlock(status, "  status| "))
	journal, _ := run(ctx, "journalctl", "-u", "immich-go", "-n", "80", "--no-pager")
	log.Raw(indentBlock(journal, "  journal| "))
	return fmt.Errorf("service did not answer on port %d within 20s: %w", cfg.Port, lastErr)
}

// smokeTestProcess boots the server directly for ~3s with the generated env
// and probes it - used whenever systemd is unavailable (containers, macOS dev).
func smokeTestProcess(ctx context.Context, cfg *InstallCfg, log *DebugLog) error {
	bin := filepath.Join(cfg.InstallDir, "immich-go")
	sctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(sctx, bin)
	cmd.Env = append(os.Environ(), envPairs(cfg)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("smoke test spawn: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var lastErr error
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := httpProbe(cfg); err == nil {
			lastErr = nil
			break
		} else {
			lastErr = err
		}
		time.Sleep(300 * time.Millisecond)
	}
	cancel()
	<-done
	if lastErr != nil {
		log.Raw(indentBlock(stderr.String(), "  stderr| "))
		return fmt.Errorf("smoke test failed: %w", lastErr)
	}
	log.Line("verify    : standalone smoke test passed (server booted & answered /api/server-info/version)")
	return nil
}

// ---------- helpers ----------

func run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func siblingBinary(serverSrc, name string) string {
	if serverSrc == "" {
		return ""
	}
	p := filepath.Join(filepath.Dir(serverSrc), name)
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// copyFile32K streams with a 32KB buffer - same discipline as the upload
// pipeline, keeps installer RSS flat even for big binaries.
func copyFile32K(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		_ = out.Close()
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()
	buf := make([]byte, 32*1024)
	if _, err := io.CopyBuffer(out, in, buf); err != nil {
		return err
	}
	if err := out.Chmod(mode); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	cleanup = false
	return os.Rename(tmp, dst)
}

func indentBlock(s, prefix string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n") + "\n"
}

func humanSize(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return "?"
	}
	const unit = 1024
	size := st.Size()
	units := []string{"B", "KiB", "MiB", "GiB"}
	v := float64(size)
	idx := 0
	for v >= unit && idx < len(units)-1 {
		v /= unit
		idx++
	}
	return fmt.Sprintf("%.1f %s", v, units[idx])
}
