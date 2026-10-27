package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

type focus int

const (
	focusCommits focus = iota
	focusFiles
)

var (
	accent = lipgloss.AdaptiveColor{Light: "#0b6e99", Dark: "#7dceff"}
	textC  = lipgloss.AdaptiveColor{Light: "#1c1c1c", Dark: "#e6e6e6"}
	muted  = lipgloss.AdaptiveColor{Light: "#5c5c5c", Dark: "#8a8f98"}
	hashC  = lipgloss.AdaptiveColor{Light: "#a15c00", Dark: "#ffd75f"}
	addC   = lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#87d787"}
	delC   = lipgloss.AdaptiveColor{Light: "#c41a16", Dark: "#ff7b72"}
	modC   = lipgloss.AdaptiveColor{Light: "#a15c00", Dark: "#ffd75f"}
	selBg  = lipgloss.AdaptiveColor{Light: "#d4e6ff", Dark: "#1f4b7a"}
	lineC  = lipgloss.AdaptiveColor{Light: "#a0a0a0", Dark: "#4a5160"}
)

type model struct {
	path       string
	root       string
	branch     string
	ready      bool
	bootErr    string
	commits    []commit
	shortW     int
	cache      map[string][]fileChange
	loading    string
	failed     string
	cursor     int
	offset     int
	fileCursor int
	fileOffset int
	focus      focus
	filter     string
	filtering  bool
	filterFrom string
	follow     string
	history    []commit
	histErr    string
	g          bool
	help       bool
	status     string
	statusErr  bool
	width      int
	height     int
}

type bootMsg struct {
	root    string
	branch  string
	commits []commit
	err     error
}

type filesMsg struct {
	hash  string
	files []fileChange
	err   error
}

type openMsg struct {
	patch string
	live  []string
	root  string
	focus string
	err   error
}

type historyMsg struct {
	path    string
	commits []commit
	err     error
}

type hunkDone struct{ err error }

type statusMsg struct {
	text string
	err  bool
}

func (m model) Init() tea.Cmd {
	return tea.Batch(tea.SetWindowTitle("hunklog"), bootstrap(m.path))
}

func bootstrap(path string) tea.Cmd {
	return func() tea.Msg {
		if path == "" {
			path = "."
		}
		if _, err := exec.LookPath("git"); err != nil {
			return bootMsg{err: fmt.Errorf("git is not on PATH")}
		}
		if _, err := exec.LookPath("hunk"); err != nil {
			return bootMsg{err: fmt.Errorf("hunk is not on PATH")}
		}
		rootB, err := gitOutput(path, "rev-parse", "--show-toplevel")
		if err != nil {
			return bootMsg{err: err}
		}
		root := strings.TrimSpace(string(rootB))
		brB, err := gitOutput(root, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			return bootMsg{err: err}
		}
		commits, err := readCommits(root)
		if err != nil {
			return bootMsg{err: err}
		}
		return bootMsg{root: root, branch: strings.TrimSpace(string(brB)), commits: commits}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		_, _, rows := m.layout()
		m.offset = reveal(m.offset, m.cursor, rows)
		m.fileOffset = reveal(m.fileOffset, m.fileCursor, rows)
		return m, nil
	case bootMsg:
		return m.onBoot(msg)
	case filesMsg:
		if msg.hash == m.loading {
			m.loading = ""
		}
		if msg.err != nil {
			m.failed = msg.hash
			m.status = msg.err.Error()
			m.statusErr = true
			return m, nil
		}
		if m.cache == nil {
			m.cache = map[string][]fileChange{}
		}
		m.cache[msg.hash] = msg.files
		if m.failed == msg.hash {
			m.failed = ""
		}
		if c, ok := m.sel(); ok && c.hash == msg.hash {
			if len(msg.files) == 0 {
				m.fileCursor = 0
			} else if m.fileCursor >= len(msg.files) {
				m.fileCursor = len(msg.files) - 1
			}
			_, _, rows := m.layout()
			m.fileOffset = reveal(m.fileOffset, m.fileCursor, rows)
		}
		return m, nil
	case historyMsg:
		if msg.path != m.follow {
			return m, nil
		}
		if msg.err != nil {
			m.histErr = msg.err.Error()
			m.history = nil
			m.status = m.histErr
			m.statusErr = true
			return m, nil
		}
		m.histErr = ""
		m.history = msg.commits
		m.cursor = 0
		m.offset = 0
		return m, m.queueFiles()
	case openMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
			m.statusErr = true
			return m, nil
		}
		var c *exec.Cmd
		var done func(error) tea.Msg
		if msg.live != nil {
			c = exec.Command("hunk", msg.live...)
			done = func(err error) tea.Msg { return hunkDone{err: err} }
		} else {
			patch := msg.patch
			c = exec.Command("hunk", "patch", patch)
			done = func(err error) tea.Msg {
				os.Remove(patch)
				return hunkDone{err: err}
			}
		}
		c.Dir = msg.root
		m.status = ""
		if msg.focus != "" {
			go focusHunkFile(msg.root, msg.focus)
		}
		return m, tea.ExecProcess(c, done)
	case hunkDone:
		if msg.err != nil {
			m.status = msg.err.Error()
			m.statusErr = true
		}
		return m, nil
	case statusMsg:
		m.status = msg.text
		m.statusErr = msg.err
		return m, nil
	case tea.MouseMsg:
		return m.onMouse(msg)
	case tea.KeyMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m model) onBoot(msg bootMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		if len(m.commits) == 0 {
			m.bootErr = msg.err.Error()
		} else {
			m.status = msg.err.Error()
			m.statusErr = true
		}
		return m, nil
	}
	prev := ""
	if c, ok := m.sel(); ok {
		prev = c.hash
	}
	m.root = msg.root
	m.branch = msg.branch
	m.commits = msg.commits
	m.history = nil
	m.histErr = ""
	m.cache = map[string][]fileChange{}
	m.loading = ""
	m.failed = ""
	m.ready = true
	m.bootErr = ""
	m.shortW = 7
	for _, c := range msg.commits {
		if n := runewidth.StringWidth(c.short); n > m.shortW {
			m.shortW = n
		}
	}
	m.cursor = 0
	m.offset = 0
	if m.follow != "" {
		_, _, rows := m.layout()
		m.offset = reveal(0, m.cursor, rows)
		return m, loadHistory(m.root, m.follow)
	}
	if prev != "" {
		for i, c := range m.visible() {
			if c.hash == prev {
				m.cursor = i
				break
			}
		}
	} else {
		m.cursor = m.firstReal()
	}
	_, _, rows := m.layout()
	m.offset = reveal(0, m.cursor, rows)
	return m, m.queueFiles()
}

func (m model) onMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	m.g = false
	if m.help {
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			m.help = false
		}
		return m, nil
	}
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		if f, _, ok := m.hit(msg.X, msg.Y); ok {
			m.focus = f
		}
		d := 1
		if msg.Button == tea.MouseButtonWheelUp {
			d = -1
		}
		return m, m.move(d)
	}
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}
	f, row, ok := m.hit(msg.X, msg.Y)
	if !ok {
		return m, nil
	}
	m.filtering = false
	m.focus = f
	_, _, rows := m.layout()
	if f == focusCommits {
		off := reveal(m.offset, m.cursor, rows)
		idx := off + row
		if idx >= len(m.visible()) {
			return m, nil
		}
		return m, m.setCursor(idx)
	}
	files, loaded := m.shownFiles()
	if !loaded {
		return m, nil
	}
	off := reveal(m.fileOffset, m.fileCursor, rows)
	idx := off + row
	if idx >= 0 && idx < len(files) {
		m.fileCursor = idx
		m.fileOffset = reveal(m.fileOffset, m.fileCursor, rows)
	}
	return m, nil
}

func (m model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if k == "ctrl+c" {
		return m, tea.Quit
	}
	if m.help {
		switch k {
		case "q":
			return m, tea.Quit
		case "?", "esc", "enter":
			m.help = false
		}
		return m, nil
	}
	if m.bootErr != "" {
		if k == "q" || k == "esc" {
			return m, tea.Quit
		}
		if k == "r" {
			m.bootErr = ""
			return m, bootstrap(m.path)
		}
		return m, nil
	}
	if m.filtering {
		return m.onFilterKey(msg)
	}
	if k != "g" {
		m.g = false
	}
	if k == "esc" && m.follow != "" {
		m.follow = ""
		m.history = nil
		m.histErr = ""
		m.cursor = m.firstReal()
		_, _, rows := m.layout()
		m.offset = reveal(0, m.cursor, rows)
		return m, m.queueFiles()
	}
	switch k {
	case "q":
		return m, tea.Quit
	case "?":
		m.help = true
		return m, nil
	case "g":
		if m.g {
			m.g = false
			return m, m.setCursor(0)
		}
		m.g = true
		return m, nil
	case "G", "end":
		return m, m.setCursor(len(m.visible()) - 1)
	case "j", "down":
		return m, m.move(1)
	case "k", "up":
		return m, m.move(-1)
	case "ctrl+d", "pgdown":
		_, _, rows := m.layout()
		return m, m.move(max(rows/2, 1))
	case "ctrl+u", "pgup":
		_, _, rows := m.layout()
		return m, m.move(-max(rows/2, 1))
	case "tab", "l", "right":
		m.focus = focusFiles
		return m, nil
	case "shift+tab", "h", "left":
		m.focus = focusCommits
		return m, nil
	case "enter":
		m.status = "opening hunk…"
		m.statusErr = false
		return m, m.openHunk(m.focus == focusFiles)
	case "c":
		m.status = "opening hunk…"
		m.statusErr = false
		return m, m.openHunk(false)
	case "/":
		m.filtering = true
		if c, ok := m.sel(); ok {
			m.filterFrom = c.hash
		}
		return m, nil
	case "f":
		return m, m.followFile()
	case "y":
		c, ok := m.sel()
		if !ok || c.kind != kindCommit {
			m.status = "no commit hash on this row"
			m.statusErr = true
			return m, nil
		}
		return m, copyHash(c.hash, c.short)
	case "r":
		m.status = "reloading…"
		m.statusErr = false
		return m, bootstrap(m.path)
	}
	return m, nil
}

func (m model) onFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filtering = false
		m.filter = ""
		return m, m.setCursor(m.indexOf(m.filterFrom))
	case "enter":
		m.filtering = false
		return m, nil
	case "backspace":
		m.filter = trimLastRune(m.filter)
		return m, m.setCursor(m.firstReal())
	case "ctrl+u":
		m.filter = ""
		return m, m.setCursor(m.firstReal())
	}
	if msg.Type == tea.KeyRunes {
		m.filter += msg.String()
		return m, m.setCursor(m.firstReal())
	}
	return m, nil
}

func trimLastRune(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return ""
	}
	return string(r[:len(r)-1])
}

func (m *model) move(d int) tea.Cmd {
	if m.focus == focusFiles {
		files, ok := m.shownFiles()
		if !ok || len(files) == 0 {
			return nil
		}
		m.fileCursor = clamp(m.fileCursor+d, 0, len(files)-1)
		_, _, rows := m.layout()
		m.fileOffset = reveal(m.fileOffset, m.fileCursor, rows)
		return nil
	}
	return m.setCursor(m.cursor + d)
}

func (m *model) setCursor(i int) tea.Cmd {
	list := m.visible()
	if len(list) == 0 {
		m.cursor = 0
		m.offset = 0
		return nil
	}
	prev := ""
	if c, ok := m.sel(); ok {
		prev = c.hash
	}
	m.cursor = clamp(i, 0, len(list)-1)
	_, _, rows := m.layout()
	m.offset = reveal(m.offset, m.cursor, rows)
	if list[m.cursor].hash != prev {
		m.fileCursor = 0
		m.fileOffset = 0
	}
	return m.queueFiles()
}

func (m *model) queueFiles() tea.Cmd {
	c, ok := m.sel()
	if !ok {
		return nil
	}
	if _, hit := m.cache[c.hash]; hit {
		return nil
	}
	if m.loading == c.hash || m.failed == c.hash {
		return nil
	}
	m.loading = c.hash
	root, hash, kind := m.root, c.hash, c.kind
	return func() tea.Msg {
		var files []fileChange
		var err error
		switch kind {
		case kindWorktree:
			files, err = statusFiles(root, false)
		case kindStaged:
			files, err = statusFiles(root, true)
		default:
			files, err = nameStatus(root, hash)
		}
		return filesMsg{hash: hash, files: files, err: err}
	}
}

func (m *model) followFile() tea.Cmd {
	if m.focus != focusFiles {
		m.status = "select a file to see its history"
		m.statusErr = true
		return nil
	}
	files, loaded := m.shownFiles()
	if !loaded || len(files) == 0 || m.fileCursor < 0 || m.fileCursor >= len(files) {
		m.status = "no file to follow"
		m.statusErr = true
		return nil
	}
	path := files[m.fileCursor].path
	if m.follow == path {
		m.follow = ""
		m.history = nil
		m.histErr = ""
		m.cursor = m.firstReal()
		_, _, rows := m.layout()
		m.offset = reveal(0, m.cursor, rows)
		m.status = ""
		return m.queueFiles()
	}
	m.follow = path
	m.history = nil
	m.histErr = ""
	m.filter = ""
	m.filtering = false
	m.cursor = 0
	m.offset = 0
	m.fileCursor = 0
	m.fileOffset = 0
	m.status = ""
	return loadHistory(m.root, path)
}

func loadHistory(root, path string) tea.Cmd {
	return func() tea.Msg {
		commits, err := readHistory(root, path)
		return historyMsg{path: path, commits: commits, err: err}
	}
}

func (m model) openHunk(jump bool) tea.Cmd {
	c, ok := m.sel()
	if !ok {
		return nil
	}
	var focus string
	if jump {
		if files, loaded := m.shownFiles(); loaded && m.fileCursor >= 0 && m.fileCursor < len(files) {
			focus = files[m.fileCursor].path
		}
	}
	root := m.root
	switch c.kind {
	case kindWorktree:
		return func() tea.Msg {
			return openMsg{root: root, focus: focus, live: []string{"diff"}}
		}
	case kindStaged:
		return func() tea.Msg {
			return openMsg{root: root, focus: focus, live: []string{"diff", "--staged"}}
		}
	}
	rev := c.hash
	return func() tea.Msg {
		patch, err := writePatch(root, rev)
		if err != nil {
			return openMsg{err: err}
		}
		return openMsg{patch: patch, root: root, focus: focus}
	}
}

// focusHunkFile moves a live hunk session onto path once the sidebar exists.
// The patch already contains every file in the commit.
// ponytail: polls for up to 4s; if hunk is not ready, the session stays on the first file.
func focusHunkFile(root, path string) {
	for range 40 {
		time.Sleep(100 * time.Millisecond)
		cmd := exec.Command("hunk", focusHunkArgs(root, path)...)
		if cmd.Run() == nil {
			return
		}
	}
}

func focusHunkArgs(root, path string) []string {
	return []string{"session", "navigate", "--repo", root, "--file", path, "--hunk", "1"}
}

// ponytail: pbcopy is macOS. Upgrade path is OSC 52 for remote terminals.
func copyHash(hash, short string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(hash)
		if err := cmd.Run(); err != nil {
			return statusMsg{text: "could not copy hash", err: true}
		}
		return statusMsg{text: "copied " + short}
	}
}

func (m model) visible() []commit {
	src := m.commits
	if m.follow != "" {
		src = m.history
	}
	out := src
	if m.filter != "" {
		q := strings.ToLower(m.filter)
		out = make([]commit, 0, len(src))
		for _, c := range src {
			if strings.Contains(strings.ToLower(c.subject), q) ||
				strings.Contains(strings.ToLower(c.hash), q) ||
				strings.Contains(strings.ToLower(c.short), q) ||
				strings.Contains(strings.ToLower(c.author), q) ||
				strings.Contains(strings.ToLower(c.date), q) ||
				strings.Contains(strings.ToLower(shortRefs(c.refs)), q) {
				out = append(out, c)
			}
		}
	}
	if m.follow != "" {
		return out
	}
	return append([]commit{worktreeRow(), stagedRow()}, out...)
}

func (m model) firstReal() int {
	for i, c := range m.visible() {
		if c.kind == kindCommit {
			return i
		}
	}
	return 0
}

func (m model) indexOf(hash string) int {
	for i, c := range m.visible() {
		if c.hash == hash {
			return i
		}
	}
	return 0
}

func (m model) sel() (commit, bool) {
	list := m.visible()
	if m.cursor < 0 || m.cursor >= len(list) {
		return commit{}, false
	}
	return list[m.cursor], true
}

func (m model) shownFiles() ([]fileChange, bool) {
	c, ok := m.sel()
	if !ok {
		return nil, true
	}
	f, ok := m.cache[c.hash]
	return f, ok
}

func (m model) layout() (left, right, rows int) {
	inner := m.width - 3
	if inner < 2 {
		return 1, 1, 1
	}
	left = inner * 3 / 5
	right = inner - left
	if right < 18 && inner > 36 {
		right = 18
		left = inner - right
	}
	if left < 1 {
		left = 1
		right = inner - left
	}
	rows = m.height - 5
	if rows < 1 {
		rows = 1
	}
	return left, right, rows
}

func (m model) hit(x, y int) (focus, int, bool) {
	left, right, rows := m.layout()
	if y < 2 || y >= 2+rows {
		return 0, 0, false
	}
	row := y - 2
	switch {
	case x >= 1 && x < 1+left:
		return focusCommits, row, true
	case x >= 2+left && x < 2+left+right:
		return focusFiles, row, true
	}
	return 0, 0, false
}

func (m model) View() string {
	if m.bootErr != "" && m.width > 0 && m.height > 0 {
		return m.errorView()
	}
	if m.help && m.width > 0 && m.height > 0 {
		return m.helpView()
	}
	if m.width < 60 || m.height < 12 {
		return "resize the terminal to use hunklog"
	}
	left, right, rows := m.layout()
	off := reveal(m.offset, m.cursor, rows)
	fileOff := reveal(m.fileOffset, m.fileCursor, rows)

	commitTitle := "Commits"
	if m.follow != "" {
		commitTitle = "History"
	}
	shownFilter := m.filter
	if runewidth.StringWidth(shownFilter) > 24 {
		shownFilter = cut(shownFilter, 24)
	}
	if m.filtering {
		commitTitle += " /" + shownFilter + "█"
	} else if m.filter != "" {
		commitTitle += " /" + shownFilter
	}
	fileTitle := "Files"
	fileMeta := ""
	if c, ok := m.sel(); ok {
		files, loaded := m.shownFiles()
		switch {
		case loaded && len(files) > 0:
			fileMeta = c.short + "  " + scrollMeta(fileOff, m.fileCursor, len(files), rows)
		case loaded || m.failed == c.hash:
			fileMeta = c.short
		default:
			fileMeta = c.short + "  …"
		}
	}

	frame := make([]string, 0, rows+2)
	frame = append(frame, styled("╭", accent)+
		hrule(left, commitTitle, scrollMeta(off, m.cursor, len(m.visible()), rows), m.focus == focusCommits)+
		styled("┬", accent)+
		hrule(right, fileTitle, fileMeta, m.focus == focusFiles)+
		styled("╮", accent))

	lc, rc, mc := lineC, lineC, lineC
	if m.focus == focusCommits {
		lc, mc = accent, accent
	} else {
		rc, mc = accent, accent
	}
	clines := m.commitLines(left, rows, off)
	flines := m.fileLines(right, rows, fileOff)
	for i := 0; i < rows; i++ {
		frame = append(frame, styled("│", lc)+clines[i]+styled("│", mc)+flines[i]+styled("│", rc))
	}
	frame = append(frame, styled("╰", lineC)+
		styled(strings.Repeat("─", left), lineC)+
		styled("┴", lineC)+
		styled(strings.Repeat("─", right), lineC)+
		styled("╯", lineC))

	lines := make([]string, 0, 1+len(frame)+2)
	lines = append(lines, m.topBar())
	lines = append(lines, frame...)
	lines = append(lines, m.summaryLine(), m.hintLine())
	return strings.Join(lines, "\n")
}

func (m model) commitLines(width, rows, off int) []string {
	lines := make([]string, rows)
	for i := range lines {
		lines[i] = blank(width)
	}
	if !m.ready {
		lines[0] = plain(width, "reading commits…", muted)
		return lines
	}
	list := m.visible()
	if m.follow != "" && m.history == nil && m.histErr == "" {
		lines[0] = plain(width, "reading history…", muted)
		return lines
	}
	if m.follow != "" && m.histErr != "" && len(list) == 0 {
		lines[0] = plain(width, "could not read history", delC)
		return lines
	}
	if len(list) == 0 {
		msg := "no commits"
		if m.follow != "" {
			msg = "no history for this file"
		}
		if m.filter != "" {
			msg = "no matching commits"
		}
		lines[0] = plain(width, msg, muted)
		return lines
	}
	for i := 0; i < rows; i++ {
		idx := off + i
		if idx >= len(list) {
			break
		}
		lines[i] = m.commitRow(list[idx], width, idx == m.cursor, m.focus == focusCommits)
	}
	return lines
}

func (m model) fileLines(width, rows, off int) []string {
	lines := make([]string, rows)
	for i := range lines {
		lines[i] = blank(width)
	}
	if !m.ready {
		return lines
	}
	c, ok := m.sel()
	if !ok {
		lines[0] = plain(width, "no commit selected", muted)
		return lines
	}
	if m.failed == c.hash {
		lines[0] = plain(width, "could not read files", delC)
		return lines
	}
	files, loaded := m.shownFiles()
	if !loaded {
		lines[0] = plain(width, "reading files…", muted)
		return lines
	}
	if len(files) == 0 {
		lines[0] = plain(width, "no file changes", muted)
		return lines
	}
	for i := 0; i < rows; i++ {
		idx := off + i
		if idx >= len(files) {
			break
		}
		lines[i] = fileRow(files[idx], width, idx == m.fileCursor, m.focus == focusFiles)
	}
	return lines
}

func (m model) commitRow(c commit, width int, current, focused bool) string {
	mark := " "
	markColor := textC
	if current && focused {
		mark = "▌"
		markColor = accent
	}
	short := fit(c.short, m.shortW)
	refs := shortRefs(c.refs)
	if refs != "" {
		refs = cut(refs, 28)
	}
	date := c.date
	need := 1 + 1 + runewidth.StringWidth(short) + 2
	if refs != "" {
		need += runewidth.StringWidth(refs) + 2
	}
	need += 1 + 10
	parts := []seg{
		{text: mark, color: markColor},
		{text: " ", color: textC},
		{text: short, color: hashC},
	}
	if refs != "" {
		parts = append(parts, seg{text: "  ", color: textC}, seg{text: refs, color: accent})
	}
	parts = append(parts, seg{text: "  ", color: textC}, seg{text: c.subject, color: textC, flex: true})
	if date != "" && width >= need {
		parts = append(parts, seg{text: " ", color: textC}, seg{text: fit(date, 10), color: muted})
	}
	return paint(width, current && focused, parts)
}

func fileRow(f fileChange, width int, current, focused bool) string {
	mark := " "
	markColor := textC
	if current && focused {
		mark = "▌"
		markColor = accent
	}
	return paint(width, current && focused, []seg{
		{text: mark, color: markColor},
		{text: " ", color: textC},
		{text: f.status, color: statusColor(f.status)},
		{text: "  ", color: textC},
		{text: f.label(), color: textC, flex: true},
	})
}

func statusColor(s string) lipgloss.TerminalColor {
	c := textC
	switch s {
	case "A", "?":
		c = addC
	case "D":
		c = delC
	case "M", "T":
		c = modC
	case "R", "C":
		c = accent
	}
	return c
}

func (m model) topBar() string {
	left := styledBold("hunklog", accent)
	if m.root != "" {
		left += styled("  "+filepath.Base(m.root), textC)
	}
	if m.branch != "" {
		left += styled("  "+m.branch, muted)
	}
	right := ""
	if m.follow != "" {
		right = styled(m.follow, muted)
	} else if m.ready {
		label := "commits"
		if len(m.commits) == 1 {
			label = "commit"
		}
		right = styled(fmt.Sprintf("%d %s", len(m.commits), label), muted)
	} else if m.bootErr == "" {
		right = styled("reading…", muted)
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return exact(left, m.width)
	}
	return exact(left+strings.Repeat(" ", gap)+right, m.width)
}

func (m model) summaryLine() string {
	left, right := "", ""
	if c, ok := m.sel(); ok {
		left = c.short + "  " + c.subject
		right = c.author + "  " + c.date
	}
	rSt := lipgloss.NewStyle().Foreground(muted)
	if m.status != "" {
		right = m.status
		rSt = lipgloss.NewStyle().Foreground(addC)
		if m.statusErr {
			rSt = lipgloss.NewStyle().Foreground(delC)
		}
	}
	if right == "" && left == "" {
		return blank(m.width)
	}
	rw := runewidth.StringWidth(right)
	if rw > m.width/2 {
		right = cut(right, m.width/2)
		rw = runewidth.StringWidth(right)
	}
	lw := m.width - rw - 1
	if lw < 1 {
		return exact(rSt.Render(fit(right, m.width)), m.width)
	}
	lSt := lipgloss.NewStyle().Foreground(textC)
	return exact(lSt.Render(fit(left, lw))+" "+rSt.Render(fit(right, rw)), m.width)
}

func (m model) hintLine() string {
	items := []hint{
		{"j/k", "move"},
		{"tab", "panel"},
		{"enter", "hunk"},
		{"f", "history"},
		{"/", "filter"},
		{"y", "hash"},
		{"r", "reload"},
		{"?", "help"},
		{"q", "quit"},
	}
	return hintLine(m.width, items)
}

func (m model) helpView() string {
	body := strings.Join([]string{
		"Commits are newest first. The top two rows are the working tree",
		"and the index. Branch and tag names sit beside the hash.",
		"enter opens every file in hunk. A selected file is focused.",
		"",
		"  j k         move",
		"  g g  G      top / bottom",
		"  ctrl-d u    half page",
		"  tab  h l    switch commits / files",
		"  enter       open in hunk",
		"  c           commit, starting at the first file",
		"  f           history of the selected file",
		"  esc         leave file history",
		"  /           filter hash, subject, author, ref",
		"  y           copy the full hash",
		"  r           reload",
		"  q           quit",
		"",
		"  commits      git show <hash> -U99999 | hunk patch -",
		"  work / index hunk diff    and    hunk diff --staged",
		"",
		"esc closes help",
	}, "\n")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Padding(1, 2).
		MaxWidth(m.width - 2).
		Render(styledBold("hunklog", accent) + "\n" + styled(body, textC))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m model) errorView() string {
	body := m.bootErr
	if m.path != "" && m.path != "." {
		body = m.path + "\n\n" + body
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(delC).
		Padding(1, 2).
		MaxWidth(max(m.width-2, 1)).
		Render(styledBold("hunklog", delC) + "\n\n" + body + "\n\nr reload    q quit")
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

type hint struct{ key, label string }

func hintLine(width int, items []hint) string {
	var b strings.Builder
	used := 0
	for _, it := range items {
		piece := styledBold(it.key, accent) + styled(" "+it.label, muted)
		w := lipgloss.Width(piece)
		gap := 0
		if used > 0 {
			gap = 3
		}
		if used+gap+w > width {
			break
		}
		if gap > 0 {
			b.WriteString(strings.Repeat(" ", gap))
			used += gap
		}
		b.WriteString(piece)
		used += w
	}
	if used < width {
		b.WriteString(strings.Repeat(" ", width-used))
	}
	return exact(b.String(), width)
}

func scrollMeta(offset, cursor, total, rows int) string {
	if total == 0 {
		return "0"
	}
	meta := fmt.Sprintf("%d/%d", cursor+1, total)
	if offset > 0 {
		meta = "↑ " + meta
	}
	if offset+rows < total {
		meta += " ↓"
	}
	return meta
}

func hrule(width int, title, meta string, active bool) string {
	if width <= 0 {
		return ""
	}
	dash := lineC
	titleColor := muted
	if active {
		dash = accent
		titleColor = accent
	}
	for runewidth.StringWidth(title) > width-2 && title != "" {
		title = cut(title, width-2)
	}
	for meta != "" && runewidth.StringWidth(title)+runewidth.StringWidth(meta)+4 > width {
		if runewidth.StringWidth(meta) <= 1 {
			meta = ""
			break
		}
		meta = cut(meta, runewidth.StringWidth(meta)-1)
	}
	fixed := 2 + runewidth.StringWidth(title) + 1
	if meta != "" {
		fixed += 1 + runewidth.StringWidth(meta)
	}
	dashes := width - fixed
	if dashes < 0 {
		return exact(styled(strings.Repeat("─", width), dash), width)
	}
	var b strings.Builder
	b.WriteString(styled("─ ", dash))
	if active {
		b.WriteString(styledBold(title, titleColor))
	} else {
		b.WriteString(styled(title, titleColor))
	}
	b.WriteString(styled(" "+strings.Repeat("─", dashes), dash))
	if meta != "" {
		b.WriteString(styled(" ", dash))
		b.WriteString(styled(meta, muted))
	}
	return exact(b.String(), width)
}

type seg struct {
	text  string
	color lipgloss.TerminalColor
	flex  bool
}

func paint(width int, selected bool, parts []seg) string {
	if width <= 0 {
		return ""
	}
	fixed := 0
	flexes := 0
	for _, p := range parts {
		if p.flex {
			flexes++
			continue
		}
		fixed += runewidth.StringWidth(p.text)
	}
	extra := width - fixed
	if extra < 0 || (flexes == 0 && extra > 0) {
		var plainText strings.Builder
		for _, p := range parts {
			plainText.WriteString(p.text)
		}
		st := lipgloss.NewStyle().Foreground(textC)
		if selected {
			st = st.Background(selBg).Bold(true)
		}
		return exact(st.Render(fit(plainText.String(), width)), width)
	}
	flexW := 0
	rem := 0
	if flexes > 0 {
		flexW = extra / flexes
		rem = extra % flexes
	}
	var b strings.Builder
	usedFlex := 0
	for _, p := range parts {
		text := p.text
		if p.flex {
			w := flexW
			if usedFlex == 0 {
				w += rem
			}
			usedFlex++
			text = fit(text, w)
		}
		st := lipgloss.NewStyle().Foreground(p.color)
		if selected {
			st = st.Background(selBg).Bold(true)
		}
		b.WriteString(st.Render(text))
	}
	return exact(b.String(), width)
}

func plain(width int, s string, c lipgloss.TerminalColor) string {
	return exact(lipgloss.NewStyle().Foreground(c).Render(fit(s, width)), width)
}

func blank(width int) string {
	if width <= 0 {
		return ""
	}
	return strings.Repeat(" ", width)
}

func styled(s string, c lipgloss.TerminalColor) string {
	return lipgloss.NewStyle().Foreground(c).Render(s)
}

func styledBold(s string, c lipgloss.TerminalColor) string {
	return lipgloss.NewStyle().Foreground(c).Bold(true).Render(s)
}

func exact(s string, n int) string {
	if n <= 0 {
		return ""
	}
	w := lipgloss.Width(s)
	if w == n {
		return s
	}
	if w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return lipgloss.NewStyle().MaxWidth(n).Render(s)
}

func fit(s string, n int) string {
	if n <= 0 {
		return ""
	}
	s = cut(s, n)
	w := runewidth.StringWidth(s)
	if w < n {
		s += strings.Repeat(" ", n-w)
	}
	return s
}

func cut(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	budget := n - 1
	var b strings.Builder
	used := 0
	for _, r := range s {
		w := runewidth.RuneWidth(r)
		if used+w > budget {
			break
		}
		b.WriteRune(r)
		used += w
	}
	b.WriteString("…")
	return b.String()
}

func reveal(offset, cursor, rows int) int {
	if rows < 1 {
		rows = 1
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor < offset {
		return cursor
	}
	if cursor >= offset+rows {
		return cursor - rows + 1
	}
	if offset < 0 {
		return 0
	}
	return offset
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
