// Debug-log exporter for the installer.
//
// ONE single file is produced per installation attempt:
//
//	immich-go-install-<timestamp>.log
//
// It aggregates everything a maintainer needs to debug a remote install
// without shell access to the board:
//   - host fingerprint (kernel, arch, CPU count, RAM, disk free)
//   - the full customised setup as resolved by the TUI (secrets masked)
//   - every step of the execution engine with timestamps and outcomes
//   - systemd / server diagnostics captured after start-up
//   - a tail of the live service journal when available
//
// The log is written atomically at the end of the run AND incrementally
// flushed so that even a hard crash mid-install leaves evidence behind.
package installer

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// DebugLog accumulates the unified debug export.
type DebugLog struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	path     string
	started  time.Time
	failedAt []string // names of steps that errored - surfaced in the header
}

// NewDebugLog creates the exporter writing to <dir>/immich-go-install-<ts>.log.
func NewDebugLog(dir string) (*DebugLog, error) {
	name := fmt.Sprintf("immich-go-install-%s.log", time.Now().Format("20060102-150405"))
	return NewDebugLogAt(dir, name)
}

// NewDebugLogAt creates the exporter at an explicit file name inside dir.
func NewDebugLogAt(dir, name string) (*DebugLog, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("debuglog: mkdir %s: %w", dir, err)
	}
	p := filepath.Join(dir, name)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("debuglog: open %s: %w", p, err)
	}
	d := &DebugLog{path: p, started: time.Now()}
	_ = f.Close()
	return d, nil
}

// Path returns the absolute path of the exported log file.
func (d *DebugLog) Path() string { return d.path }

// Section writes an uppercase banner used to separate logical blocks.
func (d *DebugLog) Section(title string) {
	d.write(fmt.Sprintf("\n==================== %s ====================\n", strings.ToUpper(title)))
}

// Line writes one timestamped line (used for step progress).
func (d *DebugLog) Line(format string, args ...any) {
	ts := time.Now().Format("2006-01-02 15:04:05")
	d.write(fmt.Sprintf("[%s] %s\n", ts, fmt.Sprintf(format, args...)))
}

// Raw writes pre-formatted multi-line content verbatim.
func (d *DebugLog) Raw(s string) { d.write(s) }

func (d *DebugLog) write(s string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// incremental flush: append-mode open keeps evidence on disk even if the
	// process dies abruptly; cost is negligible on an installer (few lines/s).
	f, err := os.OpenFile(d.path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return // last resort: never crash the installer because of its logger
	}
	defer f.Close()
	_, _ = f.WriteString(s)
}

// WriteHeader emits the preamble: versions + host fingerprint + masked config.
func (d *DebugLog) WriteHeader(cfg *InstallCfg, version string) {
	d.Section("go-immich installer debug log")
	d.Line("installer version : %s", version)
	d.Line("go runtime        : %s (%s/%s)", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	d.Line("host              : %s", hostFingerprint())
	d.Line("cpu cores         : %d", runtime.NumCPU())
	if mem := readMemTotalKB(); mem > 0 {
		d.Line("total ram         : %.1f MB", float64(mem)/1024.0)
	}
	if free := diskFreeBytes(cfg.DataDir); free > 0 {
		d.Line("free space (%s)  : %.1f GB", cfg.DataDir, float64(free)/(1<<30))
	}
	d.Line("uid               : %d (root=%v), systemd available: %v",
		os.Getuid(), cfg.HasRoot, cfg.HasSysd)

	d.Section("resolved setup (user-customised)")
	for _, kv := range cfg.SummaryLines() {
		d.Raw(kv + "\n")
	}
}

// RecordFailure notes a failed step so it shows up in the final summary.
func (d *DebugLog) RecordFailure(step string) {
	d.mu.Lock()
	d.failedAt = append(d.failedAt, step)
	d.mu.Unlock()
}

// WriteFooter emits the outcome summary and the "send us this file" note.
func (d *DebugLog) WriteFooter(ok bool) {
	d.Section("result")
	d.Line("elapsed           : %s", time.Since(d.started).Round(time.Millisecond))
	if len(d.failedAt) > 0 {
		d.Line("failed steps      : %s", strings.Join(d.failedAt, ", "))
	}
	if ok {
		d.Line("outcome           : SUCCESS")
	} else {
		d.Line("outcome           : FAILURE")
	}
	d.Raw(`
This file is the single artifact to send to the go-immich maintainers when
reporting an installation problem. It contains no plaintext secrets
(passwords / JWT are masked above). Regenerate safely at any time with:

    immich-go-installer --export-only
`)
}

// SummaryLines renders the customisable configuration for the log/TUI review
// page with secrets masked.
func (c *InstallCfg) SummaryLines() []string {
	mask := func(s string) string {
		if s == "" {
			return "(empty)"
		}
		if len(s) <= 4 {
			return "****"
		}
		return s[:2] + "****" + fmt.Sprint(len(s)) + " chars"
	}
	yesno := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	return []string{
		fmt.Sprintf("target            : %s/%s", c.GOOS, c.GOARCH),
		fmt.Sprintf("install dir       : %s", c.InstallDir),
		fmt.Sprintf("data dir          : %s", c.DataDir),
		fmt.Sprintf("http port         : %d", c.Port),
		fmt.Sprintf("admin email       : %s", c.AdminEmail),
		fmt.Sprintf("admin password    : %s", mask(c.AdminPassword)),
		fmt.Sprintf("jwt secret        : %s", mask(c.JWTSecret)),
		fmt.Sprintf("db max conns      : %d", c.MaxConns),
		fmt.Sprintf("worker interval   : %ds", c.WorkerIntervalS),
		fmt.Sprintf("worker cpu budget : %d%%", c.WorkerMaxCPU),
		fmt.Sprintf("resize concurrency: %d", c.ResizeConc),
		fmt.Sprintf("memory limit      : %d MB", c.MemLimitMB),
		fmt.Sprintf("thumbnail size    : %d px", c.ThumbSize),
		fmt.Sprintf("systemd service   : %s (start now: %s)", yesno(c.RunAsService), yesno(c.StartNow)),
		fmt.Sprintf("os tuning         : %s", yesno(c.OptimizeOS)),
	}
}
