package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lawless-commits/gh-offload/internal/github"
	"github.com/lawless-commits/gh-offload/internal/gitrepo"
	"github.com/lawless-commits/gh-offload/internal/secret"
	"github.com/lawless-commits/gh-offload/internal/shell"
	"github.com/lawless-commits/gh-offload/internal/unpack"
)

// workflowFile is the workflow's file name under .github/workflows.
const workflowFile = "offload.yml"

var runners = map[string]string{
	"linux":       "ubuntu-latest",
	"linux-arm":   "ubuntu-24.04-arm",
	"macos":       "macos-latest",
	"windows":     "windows-latest",
	"windows-arm": "windows-11-arm",
}

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

type options struct {
	os, runner, dir, remote string
	get, include            stringList
	timeout                 int
	keep, json, yes         bool
	noCache                 bool
}

// result is what --json prints.
type result struct {
	ID              string   `json:"id"`
	RunID           int64    `json:"run_id"`
	RunURL          string   `json:"run_url"`
	Runner          string   `json:"runner"`
	Command         string   `json:"command"`
	Conclusion      string   `json:"conclusion"`
	ExitCode        int      `json:"exit_code"`
	Output          string   `json:"output"`
	Files           []string `json:"files"`
	Notes           []string `json:"notes"`
	DurationSeconds float64  `json:"duration_seconds"`
}

func parseRunFlags(args []string, stderr io.Writer) (*options, []string, error) {
	fs := flag.NewFlagSet("gh offload", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	o := &options{}
	fs.StringVar(&o.os, "os", "linux", "")
	fs.StringVar(&o.runner, "runner", "", "")
	fs.Var(&o.get, "get", "")
	fs.Var(&o.include, "include", "")
	fs.IntVar(&o.timeout, "timeout", 60, "")
	fs.BoolVar(&o.noCache, "no-cache", false, "")
	fs.BoolVar(&o.keep, "keep", false, "")
	fs.BoolVar(&o.json, "json", false, "")
	fs.BoolVar(&o.yes, "yes", false, "")
	fs.BoolVar(&o.yes, "y", false, "")
	fs.StringVar(&o.dir, "C", ".", "")
	fs.StringVar(&o.remote, "remote", "origin", "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, nil, nil
		}
		return nil, nil, &exitError{code: exitUsage}
	}
	cmd := fs.Args()
	if len(cmd) == 0 {
		return nil, nil, &exitError{code: exitUsage, err: errors.New("no command given; try `gh offload -- make test` or `gh offload --help`")}
	}
	if o.timeout < 1 || o.timeout > 360 {
		return nil, nil, &exitError{code: exitUsage, err: errors.New("--timeout must be between 1 and 360 minutes")}
	}
	for _, g := range o.get {
		if g == "" || strings.ContainsAny(g, "\n\r") {
			return nil, nil, &exitError{code: exitUsage, err: fmt.Errorf("bad --get value %q", g)}
		}
	}
	return o, cmd, nil
}

func runnerLabel(osName, runner string) (string, error) {
	if runner != "" {
		return runner, nil
	}
	if label, ok := runners[osName]; ok {
		return label, nil
	}
	names := make([]string, 0, len(runners))
	for name := range runners {
		names = append(names, name)
	}
	sort.Strings(names)
	return "", fmt.Errorf("unknown --os %q; use one of %s, or --runner <label>", osName, strings.Join(names, ", "))
}

func runOffload(args []string, stdout, stderr io.Writer) error {
	o, cmdArgs, err := parseRunFlags(args, stderr)
	if err != nil || o == nil {
		return err
	}
	label, err := runnerLabel(o.os, o.runner)
	if err != nil {
		return &exitError{code: exitUsage, err: err}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	say := func(format string, a ...any) {
		if !o.json {
			fmt.Fprintf(stderr, "› "+format+"\n", a...)
		}
	}
	err = offload(ctx, o, cmdArgs, label, say, stdout, stderr)
	if ctx.Err() != nil {
		return &exitError{code: exitSignal, err: errors.New("interrupted")}
	}
	return err
}

// connect finds the GitHub repository behind a git remote.
func connect(ctx context.Context, root, remote string) (*github.Client, *github.RepoInfo, error) {
	remoteURL, err := gitrepo.Git(ctx, root, nil, nil, "remote", "get-url", remote)
	if err != nil {
		return nil, nil, fmt.Errorf("no git remote named %q (pick one with --remote)", remote)
	}
	host, repo, err := github.ParseRemote(remoteURL)
	if err != nil {
		return nil, nil, err
	}
	client := &github.Client{Host: host, Repo: repo}
	info, err := client.Info(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("looking up %s: %w", repo, err)
	}
	return client, info, nil
}

func offload(ctx context.Context, o *options, cmdArgs []string, label string, say func(string, ...any), stdout, stderr io.Writer) error {
	root, err := gitrepo.Root(ctx, o.dir)
	if err != nil {
		return err
	}
	client, info, err := connect(ctx, root, o.remote)
	if err != nil {
		return err
	}
	if err := client.CheckWorkflow(ctx, workflowFile); err != nil {
		return err
	}

	id := newID()
	snap, err := gitrepo.Take(ctx, root, "gh-offload snapshot "+id+" [skip ci]")
	if err != nil {
		return err
	}
	var flagged []string
	for _, c := range snap.Changes {
		if c.Status != "D" && secret.Looks(c.Path) && !secret.Allowed(c.Path, o.include) {
			flagged = append(flagged, c.Path)
		}
	}
	if len(flagged) > 0 {
		return fmt.Errorf("not uploading, because these files look like secrets:\n  %s\nAdd them to .gitignore, or pass --include <path> for each one you do want on the runner", strings.Join(flagged, "\n  "))
	}
	say("%s", describe(snap))
	if n, err := gitrepo.Unpushed(ctx, root, o.remote); err == nil && n > 0 {
		say("this also uploads %d local commit(s) that %s does not have yet", n, o.remote)
	}
	if !info.Private && !o.yes {
		if err := confirmPublic(client.Repo, stderr); err != nil {
			return err
		}
	}

	branch := "offload/" + id
	if err := gitrepo.Push(ctx, root, o.remote, snap.Commit, "refs/heads/"+branch, false); err != nil {
		return fmt.Errorf("pushing the snapshot branch %s: %w", branch, err)
	}
	defer func() {
		if o.keep {
			say("kept the snapshot branch %s", branch)
			return
		}
		bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.DeleteBranch(bg, branch); err != nil {
			fmt.Fprintf(stderr, "gh offload: could not delete the snapshot branch %s: %v\n", branch, err)
		}
	}()

	command := shell.Join(cmdArgs)
	color := !o.json && isTerminal(os.Stdout) && os.Getenv("NO_COLOR") == ""
	inputs := map[string]string{
		"id":      id,
		"sha":     snap.Commit,
		"runner":  label,
		"command": base64.StdEncoding.EncodeToString([]byte(command)),
		"get":     strings.Join(o.get, "\n"),
		"timeout": strconv.Itoa(o.timeout),
		"color":   strconv.FormatBool(color),
		"cache":   strconv.FormatBool(!o.noCache),
	}
	started := time.Now()
	runID, err := client.Dispatch(ctx, workflowFile, info.DefaultBranch, inputs)
	if err != nil {
		return explainDispatch(err)
	}
	if runID == 0 {
		if runID, err = client.FindRun(ctx, workflowFile, info.DefaultBranch, "offload "+id, started); err != nil {
			return err
		}
	}
	runURL := client.RunURL(runID)
	say("running on %s: %s", label, runURL)

	finished := false
	defer func() {
		if finished {
			return
		}
		bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.CancelRun(bg, runID); err == nil {
			fmt.Fprintln(stderr, "› cancelled the run")
		}
	}()
	run, err := watch(ctx, client, runID, label, time.Duration(o.timeout)*time.Minute+time.Hour, say)
	if err != nil {
		return err
	}
	finished = true

	tmp, err := os.MkdirTemp("", "gh-offload-result-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := client.DownloadArtifact(ctx, runID, "offload-"+id, tmp); err != nil {
		if log, lerr := client.FailedLog(ctx, runID); lerr == nil && len(log) > 0 {
			stderr.Write(log)
		}
		return fmt.Errorf("the run ended (%s) before the command could report back; see %s", run.Conclusion, runURL)
	}

	res := result{ID: id, RunID: runID, RunURL: runURL, Runner: label, Command: command, Conclusion: run.Conclusion, Files: []string{}, Notes: []string{}}
	note := func(format string, a ...any) {
		msg := fmt.Sprintf(format, a...)
		res.Notes = append(res.Notes, msg)
		say("%s", msg)
	}
	output, _ := os.ReadFile(filepath.Join(tmp, "output.log"))
	if o.json {
		res.Output = string(output)
	} else {
		stdout.Write(output)
	}
	if b, err := os.ReadFile(filepath.Join(tmp, "get.log")); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if line != "" {
				note("--get: %s", line)
			}
		}
	}
	if f, err := os.Open(filepath.Join(tmp, "files.tgz")); err == nil {
		ex, err := unpack.Extract(f, root)
		f.Close()
		if err != nil {
			return fmt.Errorf("unpacking the --get files: %w", err)
		}
		res.Files = ex.Files
		if len(ex.Files) > 0 {
			say("brought back %d file(s): %s", len(ex.Files), list(ex.Files, 5))
		}
		for _, s := range ex.Skipped {
			note("--get: skipped %s (links and special files are not brought back)", s)
		}
	}

	code, err := readExitCode(filepath.Join(tmp, "exit_code"))
	elapsed := time.Since(started).Round(time.Second)
	res.DurationSeconds = elapsed.Seconds()
	var failure error
	if err != nil {
		code = exitOffload
		if run.Conclusion == "cancelled" || run.Conclusion == "timed_out" {
			code = exitTimeout
			failure = fmt.Errorf("the command did not finish: the run was cancelled or hit the %d-minute --timeout", o.timeout)
		} else {
			failure = fmt.Errorf("the command did not report an exit code (run %s); see %s", run.Conclusion, runURL)
		}
	}
	res.ExitCode = code

	if o.json {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			return err
		}
	} else if failure == nil {
		say("exit code %d after %s", code, elapsed)
	}
	if code != 0 {
		return &exitError{code: code, err: failure}
	}
	return nil
}

func readExitCode(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

func explainDispatch(err error) error {
	var he *github.HTTPError
	if errors.As(err, &he) {
		switch {
		case he.Status == 422 && strings.Contains(he.Message, "nexpected inputs"):
			return fmt.Errorf("the repository's offload.yml is older than this gh-offload; run `gh offload init --force` and merge the pull request it opens (%s)", he.Message)
		case he.Status == 403 || he.Status == 404:
			return fmt.Errorf("GitHub refused to start the workflow; your token needs permission to run Actions in this repository (%s)", he.Message)
		}
	}
	return fmt.Errorf("starting the workflow: %w", err)
}

// watch polls the run until it completes, printing each step as it starts.
func watch(ctx context.Context, c *github.Client, runID int64, label string, limit time.Duration, say func(string, ...any)) (*github.Run, error) {
	start := time.Now()
	lastSaid := start
	seen := map[string]bool{}
	waiting, warned := false, false
	failures := 0
	for {
		run, err := c.Run(ctx, runID)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if failures++; failures >= 5 {
				return nil, err
			}
		} else {
			failures = 0
			if run.Status == "completed" {
				return run, nil
			}
			if run.Status == "in_progress" {
				if jobs, err := c.Jobs(ctx, runID); err == nil {
					for _, j := range jobs {
						for _, s := range j.Steps {
							if s.Status == "queued" || s.Status == "pending" || seen[s.Name] || boring(s.Name) {
								continue
							}
							seen[s.Name] = true
							say("%s", s.Name)
							lastSaid = time.Now()
						}
					}
				}
			} else if !waiting {
				waiting = true
				say("waiting for a %s runner", label)
				lastSaid = time.Now()
			}
			if run.Status != "in_progress" && !warned && time.Since(start) > 5*time.Minute {
				warned = true
				say("still queued after 5 minutes; check that %s runners are available to this repository", label)
				lastSaid = time.Now()
			}
		}
		if time.Since(lastSaid) > 2*time.Minute {
			say("still running, %s so far", time.Since(start).Round(time.Second))
			lastSaid = time.Now()
		}
		if time.Since(start) > limit {
			return nil, fmt.Errorf("gave up waiting for the run after %s", limit)
		}
		interval := 3 * time.Second
		if time.Since(start) > 2*time.Minute {
			interval = 10 * time.Second
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
	}
}

func boring(step string) bool {
	return step == "Set up job" || step == "Complete job" || strings.HasPrefix(step, "Post ")
}

func describe(snap *gitrepo.Snapshot) string {
	short := snap.Head
	if len(short) > 7 {
		short = short[:7]
	}
	if len(snap.Changes) == 0 {
		return "snapshot: HEAD (" + short + "), no local changes"
	}
	counts := map[string]int{}
	var paths []string
	for _, c := range snap.Changes {
		counts[c.Status]++
		paths = append(paths, c.Path)
	}
	var parts []string
	for _, k := range []struct{ status, word string }{{"M", "changed"}, {"T", "changed type"}, {"A", "new"}, {"D", "deleted"}} {
		if n := counts[k.status]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, k.word))
		}
	}
	return fmt.Sprintf("snapshot: HEAD (%s) + %s: %s", short, strings.Join(parts, ", "), list(paths, 5))
}

func list(items []string, max int) string {
	if len(items) <= max {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:max], ", ") + fmt.Sprintf(" and %d more", len(items)-max)
}

func confirmPublic(repo string, stderr io.Writer) error {
	msg := repo + " is public, so the snapshot branch, the command and its output will be visible to anyone"
	if !isTerminal(os.Stdin) {
		return errors.New(msg + "; pass --yes to go ahead")
	}
	fmt.Fprintf(stderr, "› %s. Continue? [y/N] ", msg)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return nil
	}
	return &exitError{code: exitOffload, err: errors.New("stopped; nothing was uploaded")}
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func newID() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}
