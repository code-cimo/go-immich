// Low-level host inspection helpers for the installer. Everything here is
// read-only and pure Go: /proc parsing + syscall.Statfs - no CGO, works on
// both amd64 and arm64 DietPi/Debian hosts as well as inside containers.
package installer

import (
	"bufio"
	"os"
	"strings"
	"syscall"
)

// hostFingerprint returns "hostname kernel" or a best-effort fallback.
func hostFingerprint() string {
	host, _ := os.Hostname()
	kernel := readFirstLine("/proc/sys/kernel/osrelease")
	if host == "" {
		host = "unknown"
	}
	if kernel == "" {
		kernel = "unknown-kernel"
	}
	return host + " / Linux " + kernel
}

// readMemTotalKB parses MemTotal from /proc/meminfo (kB). Returns 0 if absent
// (e.g. macOS dev box) so callers can skip the line gracefully.
func readMemTotalKB() int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				var v int64
				for _, ch := range fields[1] {
					if ch < '0' || ch > '9' {
						return 0
					}
					v = v*10 + int64(ch-'0')
				}
				return v
			}
		}
	}
	return 0
}

// diskFreeBytes reports free space visible to path's filesystem. Walks up to
// the nearest existing parent because the target dir may not exist yet when
// the preflight runs.
func diskFreeBytes(path string) uint64 {
	p := path
	for p != "/" && p != "" {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			break
		}
		p = strings.TrimSuffix(p, "/"+lastElem(p))
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(p, &stat); err != nil {
		return 0
	}
	return stat.Bavail * uint64(stat.Bsize)
}

func lastElem(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// readFirstLine returns the first line of a small procfs file, or "".
func readFirstLine(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if i := strings.IndexByte(string(b), '\n'); i >= 0 {
		return string(b[:i])
	}
	return strings.TrimSpace(string(b))
}

// hasSystemd detects a systemd host by checking for the pid1 socket / binary.
func hasSystemd() bool {
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		return true
	}
	if _, err := os.Stat("/sys/fs/cgroup/systemd"); err == nil {
		return true
	}
	return false
}
