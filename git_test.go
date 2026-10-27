package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestParseLog(t *testing.T) {
	in := "full\x1fshort\x1fAda\x1f2026-10-27\x1ffix it\x00full2\x1fs2\x1fBea\x1f2026-10-26\x1fother"
	cs, err := parseLog(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || cs[0].short != "short" || cs[0].subject != "fix it" || cs[1].author != "Bea" {
		t.Fatalf("%+v", cs)
	}
}

func TestParseNameStatus(t *testing.T) {
	in := "A\x00added.txt\x00R050\x00old name.txt\x00new name.txt\x00M\x00file.go\x00"
	files, err := parseNameStatus(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("%+v", files)
	}
	if files[0].status != "A" || files[0].path != "added.txt" {
		t.Fatalf("add %+v", files[0])
	}
	if files[1].status != "R" || files[1].old != "old name.txt" || files[1].path != "new name.txt" {
		t.Fatalf("rename %+v", files[1])
	}
	if files[1].label() != "old name.txt → new name.txt" {
		t.Fatalf("label %q", files[1].label())
	}
	if files[2].status != "M" || files[2].path != "file.go" {
		t.Fatalf("mod %+v", files[2])
	}
}

func TestShowArgsMatchHunkCommand(t *testing.T) {
	if got := strings.Join(showArgs("abc"), " "); got != "show abc -U99999" {
		t.Fatal(got)
	}
	if got := strings.Join(focusHunkArgs("/repo", "a.go"), " "); got != "session navigate --repo /repo --file a.go --hunk 1" {
		t.Fatal(got)
	}
}

func TestVisibleFilter(t *testing.T) {
	m := model{commits: []commit{
		{short: "aaa1111", hash: "aaa", subject: "alpha", author: "Ada"},
		{short: "bbb2222", hash: "bbb", subject: "beta", author: "Bea"},
	}, filter: "alp"}
	got := m.visible()
	if len(got) != 1 || got[0].short != "aaa1111" {
		t.Fatalf("%+v", got)
	}
}

func TestViewShowsCommitAndFile(t *testing.T) {
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	m := model{
		width:  100,
		height: 30,
		root:   "/tmp/repo",
		branch: "main",
		ready:  true,
		commits: []commit{{
			hash:    hash,
			short:   "aaaaaaa",
			author:  "Ada",
			date:    "2026-10-27",
			subject: "show the whole file",
		}},
		cache: map[string][]fileChange{
			hash: {{status: "M", path: "internal/tui/ui.go"}},
		},
		shortW: 7,
	}
	got := m.View()
	lines := strings.Split(got, "\n")
	if len(lines) != 30 {
		t.Fatalf("lines %d", len(lines))
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w != 100 {
			t.Fatalf("line %d width %d: %q", i, w, stripANSI(line))
		}
	}
	plain := stripANSI(got)
	for _, want := range []string{"hunklog", "repo", "main", "aaaaaaa", "show the whole file", "internal/tui/ui.go", "2026-10-27", "Ada"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("view missing %q\n%s", want, plain)
		}
	}

	m.filtering = true
	m.filter = "whole"
	m.focus = focusFiles
	got = m.View()
	lines = strings.Split(got, "\n")
	if len(lines) != 30 {
		t.Fatalf("filtered lines %d", len(lines))
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w != 100 {
			t.Fatalf("filtered line %d width %d: %q", i, w, stripANSI(line))
		}
	}
}

func TestGitRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.email=a@b.c", "-c", "user.name=Ada"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %s", err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(dir+"/old name.txt", []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "old name.txt")
	git("commit", "-qm", "init")
	git("mv", "old name.txt", "new name.txt")
	if err := os.WriteFile(dir+"/new name.txt", []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "new name.txt")
	if err := os.WriteFile(dir+"/other.txt", []byte("other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "other.txt")
	git("commit", "-qm", "rename and edit")

	commits, err := readCommits(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 || commits[0].subject != "rename and edit" {
		t.Fatalf("%+v", commits)
	}
	files, err := nameStatus(dir, commits[0].hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("%+v", files)
	}
	patch, err := writePatch(dir, commits[0].hash)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(patch)
	body, err := os.ReadFile(patch)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("rename and edit")) || !bytes.Contains(body, []byte("new name.txt")) || !bytes.Contains(body, []byte("+beta")) || !bytes.Contains(body, []byte("other.txt")) {
		t.Fatalf("patch missing expected diff:\n%s", body)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			continue
		}
		for i+1 < len(s) && s[i] != 'm' {
			i++
		}
	}
	return b.String()
}
