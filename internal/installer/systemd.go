// Systemd unit rendering + HTTP probe helpers used by the installer engine.
package installer

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"
)

// renderUnit produces the immich-go.service unit tuned for SBCs:
//   - Nice=19 / IOSchedulingClass=idle so background compression never
//     starves the OS on an Amlogic S905,
//   - MemoryMax as a hard OOM guard below the board's 2GB,
//   - EnvironmentFile pointing at the env written by stepEnvFile,
//   - Restart=always with a 5s backoff (matches deploy/immich-go.service).
func renderUnit(cfg *InstallCfg) string {
	bin := filepath.Join(cfg.InstallDir, "immich-go")
	env := filepath.Join(cfg.InstallDir, "immich-go.env")
	memMax := fmt.Sprintf("%dM", cfg.MemLimitMB+128) // headroom above the worker soft limit
	return fmt.Sprintf(`[Unit]
Description=go-immich - lightweight Immich-compatible server
Documentation=https://github.com/go-immich/go-immich
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s
EnvironmentFile=%s
WorkingDirectory=%s
Restart=always
RestartSec=5
# --- SBC-friendly scheduling (ODROID-C2 guidance) -------------------------
Nice=19
IOSchedulingClass=idle
MemoryMax=%s
IOWeight=10
LimitNOFILE=65536
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ReadWritePaths=%s %s

[Install]
WantedBy=multi-user.target
`, bin, env, cfg.InstallDir, memMax, cfg.DataDir, cfg.InstallDir)
}

// envPairs converts the resolved config into KEY=VALUE pairs injected into
// child processes (DB init and smoke test) without touching global state.
func envPairs(cfg *InstallCfg) []string {
	return []string{
		"IMMICH_DATA_DIR=" + cfg.DataDir,
		"IMMICH_DB_PATH=" + filepath.Join(cfg.DataDir, "immich.db"),
		"IMMICH_PHOTOS_DIR=" + filepath.Join(cfg.DataDir, "photos"),
		"IMMICH_THUMBNAILS_DIR=" + filepath.Join(cfg.DataDir, "thumbnails"),
		"IMMICH_ENCODED_DIR=" + filepath.Join(cfg.DataDir, "encoded"),
		fmt.Sprintf("IMMICH_PORT=%d", cfg.Port),
		"IMMICH_JWT_SECRET=" + cfg.JWTSecret,
		"IMMICH_ADMIN_EMAIL=" + cfg.AdminEmail,
		"IMMICH_ADMIN_PASSWORD=" + cfg.AdminPassword,
		"IMMICH_ADMIN_NAME=" + cfg.AdminName,
		fmt.Sprintf("IMMICH_DB_MAX_CONNS=%d", cfg.MaxConns),
		fmt.Sprintf("IMMICH_WORKER_INTERVAL=%ds", cfg.WorkerIntervalS),
		fmt.Sprintf("IMMICH_WORKER_MAX_CPU=%d", cfg.WorkerMaxCPU),
		fmt.Sprintf("IMMICH_RESIZE_CONCURRENCY=%d", cfg.ResizeConc),
		fmt.Sprintf("IMMICH_MEMORY_LIMIT_MB=%d", cfg.MemLimitMB),
		fmt.Sprintf("IMMICH_THUMBNAIL_SIZE=%d", cfg.ThumbSize),
	}
}

// httpProbe performs the exact handshake the Immich iOS app uses first:
// GET /api/server-info/version. Any 2xx means the stack is alive.
func httpProbe(cfg *InstallCfg) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/api/server-info/version", cfg.Port)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body) // drain so the connection can reuse/close cleanly
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}
	return nil
}
