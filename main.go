// Command gh-offload is a GitHub CLI extension that runs a command on a
// GitHub Actions runner, using the local working tree as it is right now,
// and brings back the output, the exit code and any files asked for.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
)

const (
	exitUsage   = 2
	exitTimeout = 124
	exitOffload = 125
	exitSignal  = 130
)

const usage = `gh offload runs a command on a GitHub Actions runner, using your working
tree as it is right now (uncommitted and untracked files included), and brings
back the output, the exit code and any files you ask for.

Usage:
  gh offload [flags] [--] <command> [args...]
  gh offload init [--no-pr | --print] [--force]

Examples:
  gh offload -- cargo build --release
  gh offload --os macos -- 'npm ci && npm test'
  gh offload --os linux-arm --get target/release/app -- cargo build --release

Flags:
  --os <name>        linux (default), linux-arm, macos, windows or windows-arm
  --runner <label>   any runs-on label instead of --os, such as ubuntu-24.04 or macos-15
  --get <path>       bring a file, directory or glob back into your working tree
                     (repeatable; relative to the repository root)
  --timeout <min>    job timeout in minutes (default 60, at most 360)
  --no-cache         don't restore or save the dependency and build cache
  --include <path>   upload a file even though it looks like a secret (repeatable)
  --keep             keep the snapshot branch after the run
  --json             print one JSON object on stdout instead of the output
  -y, --yes          don't ask before running in a public repository
  -C <dir>           run as if gh offload was started in <dir>
  --remote <name>    git remote to push the snapshot to (default origin)

A single argument is run by bash as written, so quotes and && work. Several
arguments are quoted one by one.

gh offload exits with the command's own exit code; 124 means the command did
not finish in time, 125 means gh offload itself failed, 130 means interrupted.

Set up a repository once with ` + "`gh offload init`" + `, which opens a pull request
adding .github/workflows/offload.yml.
`

// exitError carries an exit code, and optionally a message, up to main.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit status %d", e.code)
	}
	return e.err.Error()
}

func main() {
	os.Exit(realMain(os.Args[1:], os.Stdout, os.Stderr))
}

func realMain(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "init":
			return report(stderr, runInit(args[1:], stdout, stderr))
		case "version", "--version":
			fmt.Fprintln(stdout, "gh-offload", version())
			return 0
		case "help", "-h", "--help", "-help":
			fmt.Fprint(stdout, usage)
			return 0
		}
	}
	return report(stderr, runOffload(args, stdout, stderr))
}

func report(stderr io.Writer, err error) int {
	if err == nil {
		return 0
	}
	var ee *exitError
	if errors.As(err, &ee) {
		if ee.err != nil {
			fmt.Fprintln(stderr, "gh offload:", ee.err)
		}
		return ee.code
	}
	fmt.Fprintln(stderr, "gh offload:", err)
	return exitOffload
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return "dev"
}
