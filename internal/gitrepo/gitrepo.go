// Package gitrepo snapshots a working tree into a commit without touching the
// user's branch, index or files, and pushes commits to a remote.
package gitrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// identity is used for every commit gh-offload makes, so it works without a
// configured user.name and never needs a signing key.
var identity = []string{
	"GIT_AUTHOR_NAME=gh-offload",
	"GIT_AUTHOR_EMAIL=gh-offload@users.noreply.github.com",
	"GIT_COMMITTER_NAME=gh-offload",
	"GIT_COMMITTER_EMAIL=gh-offload@users.noreply.github.com",
}

// Git runs git in dir with extra environment variables and returns stdout
// without its trailing newline.
func Git(ctx context.Context, dir string, env []string, stdin io.Reader, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// Root returns the top directory of the working tree that contains dir.
func Root(ctx context.Context, dir string) (string, error) {
	out, err := Git(ctx, dir, nil, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not inside a git repository: %w", err)
	}
	return filepath.FromSlash(out), nil
}

// Change is one path that differs between HEAD and the snapshot.
type Change struct {
	Status string // A (new), M (modified), D (deleted) or T (type changed)
	Path   string // relative to the repository root, with forward slashes
}

// Snapshot is a commit holding the working tree as it was when it was taken.
type Snapshot struct {
	Commit  string
	Head    string
	Changes []Change
}

// Take commits the working tree, including untracked files that are not
// ignored, as a child of HEAD. It uses a throwaway index, so the user's index,
// branch and files stay exactly as they were.
func Take(ctx context.Context, root, message string) (*Snapshot, error) {
	head, err := Git(ctx, root, nil, nil, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return nil, errors.New("this repository has no commits yet; make one and push it first")
	}

	tmp, err := os.MkdirTemp("", "gh-offload-index-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	index := filepath.Join(tmp, "index")
	env := []string{"GIT_INDEX_FILE=" + index}

	// Starting from a copy of the real index lets git reuse its cached file
	// stats instead of hashing every file again. If the copy is missing or
	// unusable (a split index, for example), start again from HEAD.
	added := false
	if realIndex, err := Git(ctx, root, nil, nil, "rev-parse", "--path-format=absolute", "--git-path", "index"); err == nil {
		if copyFile(realIndex, index) == nil {
			if _, err := Git(ctx, root, env, nil, "add", "--all"); err == nil {
				added = true
			}
		}
	}
	if !added {
		os.Remove(index)
		if _, err := Git(ctx, root, env, nil, "read-tree", head); err != nil {
			return nil, err
		}
		if _, err := Git(ctx, root, env, nil, "add", "--all"); err != nil {
			return nil, err
		}
	}

	tree, err := Git(ctx, root, env, nil, "write-tree")
	if err != nil {
		return nil, err
	}
	raw, err := Git(ctx, root, env, nil, "diff-index", "--cached", "--name-status", "--no-renames", "-z", head)
	if err != nil {
		return nil, err
	}
	commit, err := Git(ctx, root, identity, nil, "commit-tree", "--no-gpg-sign", "-p", head, "-m", message, tree)
	if err != nil {
		return nil, err
	}
	return &Snapshot{Commit: commit, Head: head, Changes: parseNameStatus(raw)}, nil
}

func parseNameStatus(raw string) []Change {
	fields := strings.Split(strings.TrimRight(raw, "\x00"), "\x00")
	var changes []Change
	for i := 0; i+1 < len(fields); i += 2 {
		changes = append(changes, Change{Status: fields[i], Path: fields[i+1]})
	}
	return changes
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Unpushed counts commits reachable from HEAD that no branch of remote has.
func Unpushed(ctx context.Context, root, remote string) (int, error) {
	out, err := Git(ctx, root, nil, nil, "rev-list", "--count", "HEAD", "--not", "--remotes="+remote)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// Push sends commit to ref on remote without running local hooks.
func Push(ctx context.Context, root, remote, commit, ref string, force bool) error {
	args := []string{"push", "--no-verify", "--quiet"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, remote, commit+":"+ref)
	_, err := Git(ctx, root, nil, nil, args...)
	return err
}

// FetchBranch fetches branch from remote and returns its commit.
func FetchBranch(ctx context.Context, root, remote, branch string) (string, error) {
	if _, err := Git(ctx, root, nil, nil, "fetch", "--quiet", "--no-tags", remote, "refs/heads/"+branch); err != nil {
		return "", err
	}
	return Git(ctx, root, nil, nil, "rev-parse", "FETCH_HEAD^{commit}")
}

// CommitFile makes a commit on top of base that sets one file, without
// touching the working tree. It reports unchanged when base already has
// exactly that content.
func CommitFile(ctx context.Context, root, base, path string, content []byte, message string) (commit string, unchanged bool, err error) {
	blob, err := Git(ctx, root, nil, bytes.NewReader(content), "hash-object", "-w", "--stdin")
	if err != nil {
		return "", false, err
	}
	if existing, err := Git(ctx, root, nil, nil, "rev-parse", "--verify", "--quiet", base+":"+path); err == nil && existing == blob {
		return "", true, nil
	}

	tmp, err := os.MkdirTemp("", "gh-offload-index-")
	if err != nil {
		return "", false, err
	}
	defer os.RemoveAll(tmp)
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}
	if _, err := Git(ctx, root, env, nil, "read-tree", base); err != nil {
		return "", false, err
	}
	if _, err := Git(ctx, root, env, nil, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+path); err != nil {
		return "", false, err
	}
	tree, err := Git(ctx, root, env, nil, "write-tree")
	if err != nil {
		return "", false, err
	}
	commit, err = Git(ctx, root, identity, nil, "commit-tree", "--no-gpg-sign", "-p", base, "-m", message, tree)
	return commit, false, err
}
