package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/lawless-commits/gh-offload/internal/gitrepo"
	"github.com/lawless-commits/gh-offload/internal/workflow"
)

const initUsage = `Usage: gh offload init [flags]

Adds .github/workflows/offload.yml to the repository's default branch by
opening a pull request. GitHub only lets a workflow be started from the
command line once it is on the default branch, so merge the pull request
before the first ` + "`gh offload`" + ` run. Your working tree and branch are not touched.

Flags:
  --no-pr            write the file into the working tree instead, for you to commit
  --print            print the workflow and exit
  --force            replace an existing setup branch, or a differing local file
  --branch <name>    branch for the pull request (default gh-offload-setup)
  -C <dir>           run as if gh offload was started in <dir>
  --remote <name>    git remote to use (default origin)
`

const prBody = `This adds the workflow that [gh-offload](https://github.com/lawless-commits/gh-offload) uses to run commands on GitHub-hosted runners.

- It only runs when someone with write access starts it (` + "`workflow_dispatch`" + `), for example with ` + "`gh offload -- cargo test`" + `.
- It checks out a snapshot commit that gh offload pushes to a temporary ` + "`offload/<id>`" + ` branch, runs the command, uploads the output, exit code and any requested files as an artifact kept for 1 day, and gh offload then deletes the branch.
- The job's token has ` + "`contents: read`" + ` only, every action is pinned to a commit, and inputs reach scripts through environment variables, never through ` + "`${{ }}`" + ` inside a script.
`

func runInit(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("gh offload init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, initUsage) }
	var dir, remote, branch string
	var printOnly, noPR, force bool
	fs.StringVar(&dir, "C", ".", "")
	fs.StringVar(&remote, "remote", "origin", "")
	fs.StringVar(&branch, "branch", "gh-offload-setup", "")
	fs.BoolVar(&printOnly, "print", false, "")
	fs.BoolVar(&noPR, "no-pr", false, "")
	fs.BoolVar(&force, "force", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return &exitError{code: exitUsage}
	}
	if fs.NArg() > 0 {
		return &exitError{code: exitUsage, err: fmt.Errorf("unexpected argument %q", fs.Arg(0))}
	}
	if printOnly {
		_, err := stdout.Write(workflow.Template)
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root, err := gitrepo.Root(ctx, dir)
	if err != nil {
		return err
	}

	if noPR {
		local := filepath.Join(root, filepath.FromSlash(workflow.File))
		if existing, err := os.ReadFile(local); err == nil {
			if bytes.Equal(existing, workflow.Template) {
				fmt.Fprintf(stderr, "› %s is already up to date\n", workflow.File)
				return nil
			}
			if !force {
				return fmt.Errorf("%s already exists and differs; pass --force to overwrite it", workflow.File)
			}
		}
		if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(local, workflow.Template, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(stderr, "› wrote %s; commit it and get it onto your default branch\n", workflow.File)
		return nil
	}

	client, info, err := connect(ctx, root, remote)
	if err != nil {
		return err
	}
	base, err := gitrepo.FetchBranch(ctx, root, remote, info.DefaultBranch)
	if err != nil {
		return err
	}
	commit, unchanged, err := gitrepo.CommitFile(ctx, root, base, workflow.File, workflow.Template, "Add gh-offload workflow")
	if err != nil {
		return err
	}
	if unchanged {
		fmt.Fprintf(stderr, "› %s already has an up-to-date %s on %s; you're ready to run gh offload\n", client.Repo, workflow.File, info.DefaultBranch)
		return nil
	}
	if err := gitrepo.Push(ctx, root, remote, commit, "refs/heads/"+branch, force); err != nil {
		return fmt.Errorf("%w\n(if %s is left over from an earlier init, pass --force)", err, branch)
	}
	url, err := client.CreatePR(ctx, branch, info.DefaultBranch, "Add gh-offload workflow", prBody)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, url)
	fmt.Fprintln(stderr, "› merge that pull request, then run: gh offload -- <command>")
	return nil
}
