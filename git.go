package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type commit struct {
	hash    string
	short   string
	author  string
	date    string
	subject string
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
		if len(f) != 5 {
			return nil, fmt.Errorf("unexpected git log record")
		}
		out = append(out, commit{hash: f[0], short: f[1], author: f[2], date: f[3], subject: f[4]})
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

func readCommits(root string) ([]commit, error) {
	out, err := gitOutput(root, "log", "--date=short", "-z", "--pretty=format:%H%x1f%h%x1f%an%x1f%ad%x1f%s")
	if err != nil {
		return nil, err
	}
	return parseLog(string(out))
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
