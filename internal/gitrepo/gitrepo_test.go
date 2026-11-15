package gitrepo

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := Git(context.Background(), dir, identity, nil, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "--quiet")
	git(t, dir, "config", "core.autocrlf", "false")
	write(t, dir, ".gitignore", "ignored/\n*.log\n")
	write(t, dir, "kept.txt", "kept\n")
	write(t, dir, "edited.txt", "before\n")
	write(t, dir, "deleted.txt", "bye\n")
	write(t, dir, "forced.log", "tracked although ignored\n")
	git(t, dir, "add", ".gitignore", "kept.txt", "edited.txt", "deleted.txt")
	git(t, dir, "add", "--force", "forced.log")
	git(t, dir, "commit", "--quiet", "--no-gpg-sign", "-m", "initial")
	return dir
}

func TestTakeSnapshotsWorkingTreeAndLeavesRepoAlone(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)

	write(t, dir, "edited.txt", "after\n")
	os.Remove(filepath.Join(dir, "deleted.txt"))
	write(t, dir, "new/untracked.txt", "new\n")
	write(t, dir, "staged.txt", "staged\n")
	git(t, dir, "add", "staged.txt")
	write(t, dir, "ignored/build.bin", "ignored\n")
	write(t, dir, "debug.log", "ignored\n")

	head := git(t, dir, "rev-parse", "HEAD")
	status := git(t, dir, "status", "--porcelain", "--untracked-files=all")
	indexPath := filepath.Join(dir, ".git", "index")
	index, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}

	snap, err := Take(ctx, dir, "snapshot [skip ci]")
	if err != nil {
		t.Fatal(err)
	}
	// Read the index before anything else runs git status, which may refresh it.
	after, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, index) {
		t.Error("the real index file was modified")
	}

	files := strings.Split(git(t, dir, "ls-tree", "-r", "--name-only", snap.Commit), "\n")
	want := []string{".gitignore", "edited.txt", "forced.log", "kept.txt", "new/untracked.txt", "staged.txt"}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("snapshot files = %v, want %v", files, want)
	}
	if got := git(t, dir, "show", snap.Commit+":edited.txt"); got != "after" {
		t.Errorf("edited.txt in snapshot = %q, want %q", got, "after")
	}
	if got := git(t, dir, "rev-parse", snap.Commit+"^"); got != head {
		t.Errorf("snapshot parent = %s, want HEAD %s", got, head)
	}
	if snap.Head != head {
		t.Errorf("Snapshot.Head = %s, want %s", snap.Head, head)
	}

	var changes []string
	for _, c := range snap.Changes {
		changes = append(changes, c.Status+" "+c.Path)
	}
	sort.Strings(changes)
	wantChanges := []string{"A new/untracked.txt", "A staged.txt", "D deleted.txt", "M edited.txt"}
	if !reflect.DeepEqual(changes, wantChanges) {
		t.Errorf("changes = %v, want %v", changes, wantChanges)
	}

	if got := git(t, dir, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved to %s", got)
	}
	if got := git(t, dir, "status", "--porcelain", "--untracked-files=all"); got != status {
		t.Errorf("status changed:\n%s\nwant:\n%s", got, status)
	}
}

func TestTakeWithCleanTree(t *testing.T) {
	dir := newRepo(t)
	snap, err := Take(context.Background(), dir, "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Changes) != 0 {
		t.Errorf("changes = %v, want none", snap.Changes)
	}
	if git(t, dir, "rev-parse", snap.Commit+"^{tree}") != git(t, dir, "rev-parse", "HEAD^{tree}") {
		t.Error("clean snapshot tree differs from HEAD")
	}
}

func TestTakeWithoutCommits(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "--quiet")
	if _, err := Take(context.Background(), dir, "snapshot"); err == nil {
		t.Fatal("expected an error for a repository with no commits")
	}
}

func TestCommitFile(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	head := git(t, dir, "rev-parse", "HEAD")
	content := []byte("name: offload\n")

	commit, unchanged, err := CommitFile(ctx, dir, head, ".github/workflows/offload.yml", content, "Add workflow")
	if err != nil {
		t.Fatal(err)
	}
	if unchanged {
		t.Fatal("unchanged = true on first commit")
	}
	if got := git(t, dir, "show", commit+":.github/workflows/offload.yml"); got != "name: offload" {
		t.Errorf("file content = %q", got)
	}
	if got := git(t, dir, "show", commit+":kept.txt"); got != "kept" {
		t.Errorf("other files not kept: kept.txt = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".github")); !os.IsNotExist(err) {
		t.Error("CommitFile wrote into the working tree")
	}

	_, unchanged, err = CommitFile(ctx, dir, commit, ".github/workflows/offload.yml", content, "Add workflow")
	if err != nil {
		t.Fatal(err)
	}
	if !unchanged {
		t.Error("unchanged = false when the file already matches")
	}
}

func TestUnpushedCountsCommitsMissingFromRemote(t *testing.T) {
	ctx := context.Background()
	remote := t.TempDir()
	git(t, remote, "init", "--quiet", "--bare")
	dir := newRepo(t)
	git(t, dir, "remote", "add", "origin", remote)
	git(t, dir, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	git(t, dir, "fetch", "--quiet", "origin")
	write(t, dir, "more.txt", "more\n")
	git(t, dir, "add", "more.txt")
	git(t, dir, "commit", "--quiet", "--no-gpg-sign", "-m", "local only")

	n, err := Unpushed(ctx, dir, "origin")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("Unpushed = %d, want 1", n)
	}

	snap, err := Take(ctx, dir, "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if err := Push(ctx, dir, "origin", snap.Commit, "refs/heads/offload/test", false); err != nil {
		t.Fatal(err)
	}
	if got := git(t, remote, "rev-parse", "refs/heads/offload/test"); got != snap.Commit {
		t.Errorf("remote branch = %s, want %s", got, snap.Commit)
	}
	base, err := FetchBranch(ctx, dir, "origin", "main")
	if err != nil {
		t.Fatal(err)
	}
	if base != git(t, dir, "rev-parse", "HEAD^") {
		t.Errorf("FetchBranch = %s, want the pushed commit", base)
	}
}

func TestParseNameStatus(t *testing.T) {
	got := parseNameStatus("M\x00a b.txt\x00A\x00dir/c\x00")
	want := []Change{{"M", "a b.txt"}, {"A", "dir/c"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := parseNameStatus(""); len(got) != 0 {
		t.Errorf("empty input gave %v", got)
	}
}
