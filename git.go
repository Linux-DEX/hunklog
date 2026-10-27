package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type rowKind int

const (
	kindCommit rowKind = iota
	kindWorktree
	kindStaged
)

type commit struct {
	kind    rowKind
	hash    string
	short   string
	author  string
	date    string
	subject string
	refs    string
}

func worktreeRow() commit {
	return commit{kind: kindWorktree, hash: ":worktree", short: "work", subject: "Working tree"}
}

func stagedRow() commit {
	return commit{kind: kindStaged, hash: ":staged", short: "staged", subject: "Staged changes"}
}

// shortRefs turns git's %D decoration into a compact label list.
// "HEAD -> main, origin/main, tag: v0.1.0" becomes "HEAD main origin/main v0.1.0".
func shortRefs(d string) string {
	d = strings.TrimSpace(d)
	if d == "" {
		return ""
	}
	var labels []string
	for _, p := range strings.Split(d, ", ") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(p, "HEAD -> "); ok {
			labels = append(labels, "HEAD", rest)
			continue
		}
		if rest, ok := strings.CutPrefix(p, "tag: "); ok {
			labels = append(labels, rest)
			continue
		}
		labels = append(labels, p)
	}
	return strings.Join(labels, " ")
}

type fileChange struct {
	status string
	old    string
	path   string
}

func (f fileChange) label() string {
	if f.old != "" {
		return f.old + " → " + f.path
	}
	return f.path
}

// showArgs is the git invocation hunklog feeds to hunk.
// One commit, every file, full context: git show <rev> -U99999
func showArgs(rev string) []string {
	return []string{"show", rev, "-U99999"}
}

func gitOutput(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"--no-pager", "-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	return stdout.Bytes(), nil
}

func parseLog(s string) ([]commit, error) {
	s = strings.Trim(s, "\n\x00")
	if s == "" {
		return nil, nil
	}
	recs := strings.Split(s, "\x00")
	out := make([]commit, 0, len(recs))
	for _, rec := range recs {
		rec = strings.TrimSuffix(rec, "\n")
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		if len(f) != 6 {
			return nil, fmt.Errorf("unexpected git log record")
		}
		out = append(out, commit{hash: f[0], short: f[1], author: f[2], date: f[3], subject: f[4], refs: f[5]})
	}
	return out, nil
}

func parseNameStatus(s string) ([]fileChange, error) {
	s = strings.Trim(s, "\n\x00")
	if s == "" {
		return nil, nil
	}
	p := strings.Split(s, "\x00")
	var out []fileChange
	for i := 0; i < len(p); {
		code := p[i]
		if code == "" {
			return nil, fmt.Errorf("unexpected name-status record")
		}
		i++
		kind := code[:1]
		if kind == "R" || kind == "C" {
			if i+1 >= len(p) {
				return nil, fmt.Errorf("truncated rename in name-status")
			}
			out = append(out, fileChange{status: kind, old: p[i], path: p[i+1]})
			i += 2
			continue
		}
		if i >= len(p) {
			return nil, fmt.Errorf("truncated name-status record")
		}
		out = append(out, fileChange{status: kind, path: p[i]})
		i++
	}
	return out, nil
}

func nameStatus(root, rev string) ([]fileChange, error) {
	out, err := gitOutput(root, "show", "-M", "--name-status", "--pretty=format:", "-z", rev)
	if err != nil {
		return nil, err
	}
	return parseNameStatus(string(out))
}

const logFormat = "%H%x1f%h%x1f%an%x1f%ad%x1f%s%x1f%D"

func readCommits(root string) ([]commit, error) {
	out, err := gitOutput(root, "log", "--date=short", "-z", "--pretty=format:"+logFormat)
	if err != nil {
		return nil, err
	}
	return parseLog(string(out))
}

func readHistory(root, path string) ([]commit, error) {
	out, err := gitOutput(root, "log", "--follow", "--date=short", "-z", "--pretty=format:"+logFormat, "--", path)
	if err != nil {
		return nil, err
	}
	return parseLog(string(out))
}

func statusFiles(root string, staged bool) ([]fileChange, error) {
	out, err := gitOutput(root, "status", "--porcelain=v1", "-z")
	if err != nil {
		return nil, err
	}
	st, work := parsePorcelain(string(out))
	if staged {
		return st, nil
	}
	return work, nil
}

// parsePorcelain splits `git status --porcelain=v1 -z`.
// A rename is the new path, then a NUL, then the old path.
func parsePorcelain(s string) (staged, work []fileChange) {
	s = strings.Trim(s, "\x00")
	if s == "" {
		return nil, nil
	}
	p := strings.Split(s, "\x00")
	for i := 0; i < len(p); {
		rec := p[i]
		i++
		if len(rec) < 4 || rec[2] != ' ' {
			continue
		}
		x, y := rec[0], rec[1]
		path := rec[3:]
		old := ""
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			if i < len(p) {
				old = p[i]
				i++
			}
		}
		if x == '?' && y == '?' {
			work = append(work, fileChange{status: "?", path: path})
			continue
		}
		if x != ' ' && x != '?' {
			st := fileChange{status: string(x), path: path}
			if x == 'R' || x == 'C' {
				st.old = old
			}
			staged = append(staged, st)
		}
		if y != ' ' && y != '?' {
			st := fileChange{status: string(y), path: path}
			if y == 'R' || y == 'C' {
				st.old = old
			}
			work = append(work, st)
		}
	}
	return staged, work
}

// writePatch stores `git show <rev> -U99999` for hunk.
// Every file in the commit is in the patch, so hunk's sidebar lists them all.
// hunk reads stdin when the argument is "-", so a file keeps the TTY free.
// ponytail: one temp file per open; removed when hunk exits.
func writePatch(root, rev string) (string, error) {
	f, err := os.CreateTemp("", "hunklog-*.patch")
	if err != nil {
		return "", err
	}
	name := f.Name()
	cmd := exec.Command("git", append([]string{"--no-pager", "-C", root}, showArgs(rev)...)...)
	cmd.Stdout = f
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	closeErr := f.Close()
	if runErr != nil {
		os.Remove(name)
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = runErr.Error()
		}
		return "", errors.New(msg)
	}
	if closeErr != nil {
		os.Remove(name)
		return "", closeErr
	}
	return name, nil
}
