// Bubble Tea TUI for the go-immich installer.
//
// Navigation model (kept deliberately simple for SSH-over-WiFi sessions):
//
//	Welcome ─▶ Setup form (grouped fields, every value user-customisable)
//	        ─▶ Review & install ─▶ live Progress ─▶ Done (debug log path shown)
//
// Keys: tab/→ next field · shift-tab/← previous · up/down choose · enter
// advance/toggle · [ ] adjust numbers · ctrl+s save+continue · q quit.
package installer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------- visual style ----------

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("204")).MarginBottom(1)
	hdrStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("51"))
	labelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	valStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231"))
	onStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	offStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("160"))
	runStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	panelStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
			 BorderForeground(lipgloss.Color("240")).Padding(0, 2)
)

// ---------- messages ----------

type progressMsg struct{ ev ProgressEvent }
type finishedMsg struct {
	err     error
	logPath string
}

// page enumerates the wizard states.
type page int

const (
	pageWelcome page = iota
	pageSetup
	pageReview
	pageProgress
	pageDone
)

// field descriptors for the setup form; grouped by section header.
type fieldKind int

const (
	fText fieldKind = iota
	fInt
	fToggle
	fSecret // text, masked in review/log
)

type field struct {
	label string
	help  string
	kind  fieldKind
	min   int // fInt bounds
	max   int
	get   func(*InstallCfg) string
	setS  func(*InstallCfg, string)
	setI  func(*InstallCfg, int)
	setB  func(*InstallCfg, bool)
	group string
}

func buildFields() []field {
	return []field{
		{group: "TARGET LAYOUT", label: "Install directory", help: "binaries + env file land here",
			kind: fText, get: cget(func(c *InstallCfg) string { return c.InstallDir }),
			setS: cset(func(c *InstallCfg, v string) { c.InstallDir = v })},
		{group: "TARGET LAYOUT", label: "Data directory", help: "photos/thumbnails/db root",
			kind: fText, get: cget(func(c *InstallCfg) string { return c.DataDir }),
			setS: cset(func(c *InstallCfg, v string) { c.DataDir = v })},

		{group: "NETWORK", label: "HTTP port", help: "Immich default is 2283",
			kind: fInt, min: 1, max: 65535,
			get:  cget(func(c *InstallCfg) string { return strconv.Itoa(c.Port) }),
			setI: csetI(func(c *InstallCfg, v int) { c.Port = v })},

		{group: "ADMIN ACCOUNT", label: "Admin email", help: "used by the Immich app to log in",
			kind: fText, get: cget(func(c *InstallCfg) string { return c.AdminEmail }),
			setS: cset(func(c *InstallCfg, v string) { c.AdminEmail = strings.TrimSpace(v) })},
		{group: "ADMIN ACCOUNT", label: "Admin password", help: "min 8 chars (bcrypt-hashed on first boot)",
			kind: fSecret, get: cget(func(c *InstallCfg) string { return c.AdminPassword }),
			setS: cset(func(c *InstallCfg, v string) { c.AdminPassword = v })},
		{group: "ADMIN ACCOUNT", label: "Display name", help: "shown in the mobile app",
			kind: fText, get: cget(func(c *InstallCfg) string { return c.AdminName }),
			setS: cset(func(c *InstallCfg, v string) { c.AdminName = v })},
		{group: "ADMIN ACCOUNT", label: "JWT secret", help: "leave blank to auto-generate 256-bit",
			kind: fSecret, get: cget(func(c *InstallCfg) string { return c.JWTSecret }),
			setS: cset(func(c *InstallCfg, v string) { c.JWTSecret = v })},

		{group: "RESOURCE TUNING", label: "DB max connections", help: "SQLite WAL pool (2..50)",
			kind: fInt, min: 2, max: 50,
			get:  cget(func(c *InstallCfg) string { return strconv.Itoa(c.MaxConns) }),
			setI: csetI(func(c *InstallCfg, v int) { c.MaxConns = v })},
		{group: "RESOURCE TUNING", label: "Worker interval (s)", help: "compression tick delay",
			kind: fInt, min: 1, max: 3600,
			get:  cget(func(c *InstallCfg) string { return strconv.Itoa(c.WorkerIntervalS) }),
			setI: csetI(func(c *InstallCfg, v int) { c.WorkerIntervalS = v })},
		{group: "RESOURCE TUNING", label: "Worker CPU budget (%)", help: "soft cap for background jobs",
			kind: fInt, min: 1, max: 100,
			get:  cget(func(c *InstallCfg) string { return strconv.Itoa(c.WorkerMaxCPU) }),
			setI: csetI(func(c *InstallCfg, v int) { c.WorkerMaxCPU = v })},
		{group: "RESOURCE TUNING", label: "Resize concurrency", help: "parallel image encodes (SBC: keep <=2)",
			kind: fInt, min: 1, max: 16,
			get:  cget(func(c *InstallCfg) string { return strconv.Itoa(c.ResizeConc) }),
			setI: csetI(func(c *InstallCfg, v int) { c.ResizeConc = v })},
		{group: "RESOURCE TUNING", label: "Memory limit (MB)", help: "worker back-off threshold",
			kind: fInt, min: 64, max: 65536,
			get:  cget(func(c *InstallCfg) string { return strconv.Itoa(c.MemLimitMB) }),
			setI: csetI(func(c *InstallCfg, v int) { c.MemLimitMB = v })},
		{group: "RESOURCE TUNING", label: "Thumbnail size (px)", help: "gallery edge length",
			kind: fInt, min: 64, max: 1024,
			get:  cget(func(c *InstallCfg) string { return strconv.Itoa(c.ThumbSize) }),
			setI: csetI(func(c *InstallCfg, v int) { c.ThumbSize = v })},

		{group: "BEHAVIOUR", label: "Install systemd service", help: "requires root; skipped otherwise",
			kind: fToggle, get: cget(func(c *InstallCfg) string { return bstr(c.RunAsService) }),
			setB: csetB(func(c *InstallCfg, v bool) { c.RunAsService = v })},
		{group: "BEHAVIOUR", label: "Start service immediately", help: "systemctl restart after install",
			kind: fToggle, get: cget(func(c *InstallCfg) string { return bstr(c.StartNow) }),
			setB: csetB(func(c *InstallCfg, v bool) { c.StartNow = v })},
		{group: "BEHAVIOUR", label: "Apply OS tuning (sysctl)", help: "swappiness/cache pressure for SBCs",
			kind: fToggle, get: cget(func(c *InstallCfg) string { return bstr(c.OptimizeOS) }),
			setB: csetB(func(c *InstallCfg, v bool) { c.OptimizeOS = v })},
	}
}

func bstr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// tiny accessor helpers to keep buildFields readable
func cget(f func(*InstallCfg) string) func(*InstallCfg) string       { return f }
func cset(f func(*InstallCfg, string)) func(*InstallCfg, string)      { return f }
func csetI(f func(*InstallCfg, int)) func(*InstallCfg, int)           { return f }
func csetB(f func(*InstallCfg, bool)) func(*InstallCfg, bool)         { return f }

// ---------- model ----------

type Model struct {
	cfg     *InstallCfg
	version string
	fields  []field

	page    page
	cursor  int   // focused field index
	editing bool  // inline text editing active
	input   string

	events chan ProgressEvent // engine -> TUI bridge
	done   chan finishedMsg

	progIdx  int
	progOK   []string // rendered step lines
	finalErr error
	logPath  string
	width    int
	height   int
}

// NewModel builds the wizard. cfg must already carry detected host facts.
func NewModel(cfg *InstallCfg, version string) *Model {
	m := &Model{cfg: cfg, version: version, fields: buildFields()}
	cfg.GOARCH = detectArch()
	cfg.GOOS = detectOS()
	cfg.HasRoot = os.Geteuid() == 0
	cfg.HasSysd = hasSystemd()
	return m
}

func (m *Model) Init() tea.Cmd { return nil }

// FinalError exposes the terminal installation outcome so cmd/installer can
// translate it into a proper process exit code after the TUI closes.
func (m *Model) FinalError() error { return m.finalErr }

// LogPath exposes the exported debug log location for scripting wrappers.
func (m *Model) LogPath() string { return m.logPath }

// ---------- update ----------

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.page == pageProgress {
			return m, nil // no interaction while installing
		}
		key := msg.String()
		if key == "ctrl+c" || (key == "q" && !(m.page == pageSetup && m.editing)) {
			return m, tea.Quit
		}

		switch m.page {
		case pageWelcome:
			if msg.String() == "enter" {
				m.page = pageSetup
			}
		case pageSetup:
			return m.updateSetup(msg)
		case pageReview:
			if msg.String() == "enter" || msg.String() == "y" {
				return m.startInstall()
			}
			if msg.String() == "left" || msg.String() == "b" {
				m.page = pageSetup
			}
		case pageDone:
			if msg.String() == "enter" {
				return m, tea.Quit
			}
			if msg.String() == "r" {
				m.page = pageSetup
				m.progOK = nil
				m.finalErr = nil
			}
		}

	case progressMsg:
		m.renderProgress(msg.ev)
		return m, m.waitForEvent()

	case finishedMsg:
		m.finalErr = msg.err
		m.logPath = msg.logPath
		m.page = pageDone
		return m, nil
	}
	return m, nil
}

func (m *Model) updateSetup(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.fields[m.cursor]
	if m.editing {
		switch msg.String() {
		case "enter":
			m.commitInput(f)
			m.editing = false
			m.nextField()
			return m, m.validateCmd()
		case "esc":
			m.editing = false
			m.input = ""
			return m, nil
		case "backspace":
			if len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
			return m, nil
		default:
			r := runeOrEmpty(msg)
			if r != 0 {
				m.input += string(r)
			}
			return m, nil
		}
	}

	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.fields)-1 {
			m.cursor++
		}
	case "right", "tab":
		m.nextField()
	case "shift+tab", "left":
		if m.cursor > 0 {
			m.cursor--
		}
	case "enter", " ":
		if f.kind == fToggle {
			cur := f.get(m.cfg) == "yes"
			f.setB(m.cfg, !cur)
		} else {
			m.editing = true
			m.input = f.get(m.cfg)
		}
	case "[":
		if f.kind == fInt {
			m.bumpInt(f, -stepFor(f))
		}
	case "]":
		if f.kind == fInt {
			m.bumpInt(f, stepFor(f))
		}
	}
	return m, m.validateCmd()
}

func (m *Model) nextField() {
	if m.cursor < len(m.fields)-1 {
		m.cursor++
	} else {
		m.page = pageReview
	}
}

func stepFor(f field) int {
	if f.max <= 100 {
		return 1
	}
	return 16
}

func (m *Model) bumpInt(f field, d int) {
	v, _ := strconv.Atoi(f.get(m.cfg))
	v += d
	if v < f.min {
		v = f.min
	}
	if v > f.max {
		v = f.max
	}
	f.setI(m.cfg, v)
}

func (m *Model) commitInput(f field) {
	switch f.kind {
	case fInt:
		if v, err := strconv.Atoi(strings.TrimSpace(m.input)); err == nil {
			if v < f.min {
				v = f.min
			}
			if v > f.max {
				v = f.max
			}
			f.setI(m.cfg, v)
		}
	default:
		f.setS(m.cfg, m.input)
	}
	m.input = ""
}

// validateCmd surfaces live validation problems on the review page; cheap.
func (m *Model) validateCmd() tea.Cmd { return nil }

func runeOrEmpty(msg tea.KeyMsg) rune {
	rs := []rune(msg.String())
	if len(rs) == 1 {
		return rs[0]
	}
	return 0
}

// ---------- install bridge ----------

func (m *Model) startInstall() (tea.Model, tea.Cmd) {
	m.page = pageProgress
	m.progOK = nil
	m.events = make(chan ProgressEvent, 64)
	m.done = make(chan finishedMsg, 1)

	emit := func(ev ProgressEvent) { m.events <- ev }

	cfg := m.cfg
	version := m.version
	events := m.events
	done := m.done

	go func() {
		defer close(events)
		// Debug export lives next to the install tree so it survives reboots
		// and is easy to scp off the board; falls back to TempDir when the
		// target is not writable (unprivileged dev runs).
		logDir := filepath.Join(cfg.DataDir, "logs")
		if err := os.MkdirAll(logDir, 0o750); err != nil {
			logDir = os.TempDir()
		}
		dlog, err := NewDebugLog(logDir)
		if err != nil {
			done <- finishedMsg{err: fmt.Errorf("cannot create debug log: %w", err)}
			return
		}
		dlog.WriteHeader(cfg, version)
		eng := NewEngine(cfg, dlog, emit)
		err = eng.Run(context.Background())
		done <- finishedMsg{err: err, logPath: dlog.Path()}
	}()

	return m, m.waitForEvent()
}

func (m *Model) waitForEvent() tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-m.events
		if !ok {
			// events closed: wait for the terminal result
			res, ok := <-m.done
			if !ok {
				return finishedMsg{err: fmt.Errorf("installer crashed unexpectedly")}
			}
			return res
		}
		return progressMsg{ev: ev}
	}
}

func (m *Model) renderProgress(ev ProgressEvent) {
	icon := map[string]string{"running": "…", "ok": "✔", "fail": "✘", "skip": "–"}[ev.Status]
	line := fmt.Sprintf("%s %s", icon, ev.Step)
	switch ev.Status {
	case "ok":
		line += dimStyle.Render(fmt.Sprintf(" (%s)", ev.Elapsed.Round(time.Millisecond)))
	case "skip":
		line += dimStyle.Render(" (" + ev.Detail + ")")
	case "fail":
		line += errStyle.Render(" " + ev.Detail)
	}
	// replace the trailing "running" line, else append
	if ev.Status == "running" {
		m.progOK = append(m.progOK, line)
	} else if n := len(m.progOK); n > 0 && strings.Contains(m.progOK[n-1], "…") {
		m.progOK[n-1] = line
	} else {
		m.progOK = append(m.progOK, line)
	}
}

// ---------- view ----------

func (m *Model) View() string {
	var body string
	switch m.page {
	case pageWelcome:
		body = m.viewWelcome()
	case pageSetup:
		body = m.viewSetup()
	case pageReview:
		body = m.viewReview()
	case pageProgress:
		body = m.viewProgress()
	case pageDone:
		body = m.viewDone()
	}
	footer := dimStyle.Render("immich-go installer " + m.version + " · " + m.cfg.GOOS + "/" + m.cfg.GOARCH +
		" · root:" + bstr(m.cfg.HasRoot) + " · systemd:" + bstr(m.cfg.HasSysd))
	return lipgloss.JoinVertical(lipgloss.Left, "", titleStyle.Render("⚡ go-immich · Immich-compatible Go server"), body, "", footer)
}

func (m *Model) viewWelcome() string {
	lines := []string{
		"This wizard installs a lightweight, Immich-API-compatible photo server.",
		"",
		"  • fully customisable setup (port, paths, admin account, resource limits)",
		"  • one static binary - runs identically on linux/amd64 and linux/arm64",
		"  • exports a single debug log you can send to the maintainers",
		"",
		hdrStyle.Render("Press ENTER to begin configuration  ·  q to quit"),
	}
	return panelStyle.Render(strings.Join(lines, "\n"))
}

func (m *Model) viewSetup() string {
	var b strings.Builder
	lastGroup := ""
	for i, f := range m.fields {
		if f.group != lastGroup {
			lastGroup = f.group
			b.WriteString(hdrStyle.Render("\n"+f.group) + "\n")
		}
		val := f.get(m.cfg)
		shown := val
		switch f.kind {
		case fSecret:
			if shown == "" {
				shown = "(auto)"
			} else {
				shown = "••••••••"
			}
		case fToggle:
			if val == "yes" {
				shown = onStyle.Render("[x] enabled")
			} else {
				shown = offStyle.Render("[ ] disabled")
			}
		}
		cursor := "  "
		if i == m.cursor {
			cursor = hdrStyle.Render("> ")
			if m.editing {
				shown = valStyle.Render(m.input) + runStyle.Render("▏")
			} else {
				shown = valStyle.Render(shown)
			}
		} else {
			shown = labelStyle.Render(shown)
		}
		fmt.Fprintf(&b, "%s%-26s %s\n", cursor, labelStyle.Render(f.label+":"), shown)
		if i == m.cursor {
			fmt.Fprintf(&b, "  %s\n", dimStyle.Render(f.help))
		}
	}
	hint := dimStyle.Render("\nenter/space edit · [ ] adjust · tab next · enter on last field = review · b back")
	return panelStyle.Render(b.String() + hint)
}

func (m *Model) viewReview() string {
	var b strings.Builder
	b.WriteString(hdrStyle.Render("REVIEW YOUR SETUP") + "\n\n")
	for _, l := range m.cfg.SummaryLines() {
		parts := strings.SplitN(l, ":", 2)
		if len(parts) == 2 {
			fmt.Fprintf(&b, "%s:%s\n", labelStyle.Render(parts[0]+":"), valStyle.Render(strings.TrimLeft(parts[1], " ")))
		} else {
			b.WriteString(l + "\n")
		}
	}
	if errs := m.cfg.Validate(); len(errs) > 0 {
		b.WriteString("\n" + errStyle.Render("Fix these before installing:") + "\n")
		for _, e := range errs {
			b.WriteString("  ✘ " + e + "\n")
		}
	} else {
		b.WriteString("\n" + onStyle.Render("Configuration looks good.") + "\n")
	}
	b.WriteString("\n" + hdrStyle.Render("ENTER = install now") + dimStyle.Render(" · b = back · q = abort")+"\n")
	b.WriteString(dimStyle.Render("A debug log will be exported under /tmp/immich-go-install-*.log"))
	return panelStyle.Render(b.String())
}

func (m *Model) viewProgress() string {
	var b strings.Builder
	b.WriteString(hdrStyle.Render("INSTALLING…") + " (do not close this terminal)\n\n")
	for _, l := range m.progOK {
		b.WriteString(l + "\n")
	}
	b.WriteString(dimStyle.Render("\nprogress mirrors the exported debug log line-by-line"))
	return panelStyle.Render(b.String())
}

func (m *Model) viewDone() string {
	var b strings.Builder
	if m.finalErr == nil {
		b.WriteString(onStyle.Render("✔ INSTALLATION COMPLETE") + "\n\n")
		fmt.Fprintf(&b, "%s API endpoint : http://<this-host>:%d/api\n", labelStyle.Render(""), m.cfg.Port)
		fmt.Fprintf(&b, "%s login        : %s (points the Immich iOS/Android app here)\n", labelStyle.Render(""), m.cfg.AdminEmail)
	} else {
		b.WriteString(errStyle.Render("✘ INSTALLATION FAILED") + "\n\n")
		b.WriteString(wrap(m.finalErr.Error(), 72) + "\n")
	}
	if m.logPath != "" {
		fmt.Fprintf(&b, "\n%s debug export : %s\n", labelStyle.Render(""), valStyle.Render(m.logPath))
		b.WriteString(dimStyle.Render("  → send this single file to the maintainers for remote debugging\n"))
		b.WriteString(dimStyle.Render("  → retrieve it with: scp " + m.logPath + " dev@laptop:/tmp/"))
	}
	b.WriteString("\n" + hdrStyle.Render("ENTER = exit") + dimStyle.Render(" · r = reconfigure"))
	return panelStyle.Render(b.String())
}

func wrap(s string, w int) string {
	words := strings.Fields(s)
	var out []string
	line := ""
	for _, word := range words {
		if len(line)+len(word)+1 > w {
			out = append(out, line)
			line = word
		} else {
			if line != "" {
				line += " "
			}
			line += word
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
