# hunklog

hunklog is a terminal UI for reading Git history one commit at a time. Commits are listed newest first. The commit you select shows the files it changed. Opening it hands the whole commit to [hunk](https://github.com/modem-dev/hunk): every changed file is in hunk's sidebar, and each file is shown in full, not only the changed lines.

That view is:

```bash
git show <hash> -U99999 | hunk patch -
```

`-U99999` asks Git for a huge unified diff, so hunk can show each complete file with the commit's edits in place. Choosing a file in hunklog still opens that same commit-wide patch, then moves hunk to the file you picked. Quitting hunk returns you to the same place in the list.

The layout is two panes, in the style of lazygit: commits on the left, files on the right, keys along the bottom. The first two rows are the working tree and the index. Branch and tag names are shown next to the commit hash.

## Before you install

`go install` builds hunklog only. Git and hunk are separate programs it calls while you use it.

- [Go](https://go.dev/dl/) 1.27 or newer
- [Git](https://git-scm.com/) on your `PATH` (also used by `go install` to download this module)
- [hunk](https://github.com/modem-dev/hunk) on your `PATH`

Copying a hash (`y`) uses `pbcopy`, which is available on macOS.

## Install

Install the latest tagged release:

```bash
go install github.com/Linux-DEX/hunklog@latest
```

Or install this release exactly:

```bash
go install github.com/Linux-DEX/hunklog@v0.2.0
```

Releases are Git tags named `vMAJOR.MINOR.PATCH`. `@latest` selects the highest of those tags.

The binary is placed in `$GOBIN`, or in `$(go env GOPATH)/bin` when `GOBIN` is unset. That directory has to be on your `PATH`:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
hunklog
```

From inside a repository, `hunklog` reads that repo. Pass a path to read another one:

```bash
hunklog /path/to/repo
```

## Run from source

```bash
git clone https://github.com/Linux-DEX/hunklog.git
cd hunklog
go run .
```

## Keys

| Key | Action |
| --- | --- |
| `j` / `k` or arrows | Move in the focused pane |
| `ctrl-d` / `ctrl-u` | Half page |
| `gg` / `G` | Top / bottom |
| `tab`, `h` / `l` | Switch between commits and files |
| `enter` | Open the selection in hunk. A commit opens every file in that commit. The working-tree row runs `hunk diff`. The staged row runs `hunk diff --staged`. A selected file is focused |
| `c` | Open the commit, starting at the first file |
| `f` | On a file, list only the commits that touched it. `f` again, or `esc`, returns to the full history |
| `/` | Filter by hash, subject, author, date, or ref. `esc` clears it |
| `y` | Copy the full hash (`pbcopy`) |
| `r` | Reload the repository |
| click | Select a row. The scroll wheel moves the pane under the pointer |
| `?` | Help |
| `q` | Quit |

File status letters follow Git: `A` added, `M` modified, `D` deleted, `R` renamed, `C` copied. A rename is shown as `old → new`.
