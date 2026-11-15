// Package github talks to GitHub through the gh CLI, so gh-offload uses
// whatever account and host `gh auth` is already set up for.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Client acts on one repository.
type Client struct {
	Host string // github.com or a GitHub Enterprise host
	Repo string // owner/name
}

// HTTPError is an API error that gh reported with a status code.
type HTTPError struct {
	Status  int
	Message string
}

func (e *HTTPError) Error() string { return e.Message }

// IsStatus reports whether err is an API error with the given status.
func IsStatus(err error, status int) bool {
	var he *HTTPError
	return errors.As(err, &he) && he.Status == status
}

var httpStatus = regexp.MustCompile(`\(HTTP (\d{3})\)`)

// ParseRemote reads the host and owner/name from a git remote URL.
func ParseRemote(raw string) (host, repo string, err error) {
	s := strings.TrimSpace(raw)
	var path string
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", "", fmt.Errorf("can't read remote URL %q: %w", raw, err)
		}
		host, path = u.Hostname(), u.Path
	} else if i := strings.Index(s, ":"); i > 0 && !strings.Contains(s[:i], "/") {
		// scp-like syntax: [user@]host:owner/name.git
		host, path = s[:i], s[i+1:]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
	} else {
		return "", "", fmt.Errorf("remote %q is not a GitHub repository URL", raw)
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("remote %q is not a GitHub repository URL", raw)
	}
	host = strings.ToLower(host)
	if host == "ssh.github.com" || host == "www.github.com" {
		host = "github.com"
	}
	return host, parts[0] + "/" + parts[1], nil
}

// RepoArg is the repository in the form gh's -R flag takes.
func (c *Client) RepoArg() string {
	if c.Host == "github.com" {
		return c.Repo
	}
	return c.Host + "/" + c.Repo
}

func (c *Client) path(p string) string { return "repos/" + c.Repo + "/" + p }

func (c *Client) gh(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errors.New("the GitHub CLI (gh) is not installed: https://cli.github.com")
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if m := httpStatus.FindStringSubmatch(msg); m != nil {
			status, _ := strconv.Atoi(m[1])
			return nil, &HTTPError{Status: status, Message: msg}
		}
		return nil, fmt.Errorf("gh %s: %s", args[0], msg)
	}
	return stdout.Bytes(), nil
}

func (c *Client) api(ctx context.Context, method, path string, body, out any) error {
	args := []string{"api", "--hostname", c.Host, "-X", method, path, "-H", "Accept: application/vnd.github+json"}
	var stdin io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		args = append(args, "--input", "-")
		stdin = bytes.NewReader(b)
	}
	stdout, err := c.gh(ctx, stdin, args...)
	if err != nil {
		return err
	}
	if out != nil && len(bytes.TrimSpace(stdout)) > 0 {
		return json.Unmarshal(stdout, out)
	}
	return nil
}

// RepoInfo is the part of a repository's metadata gh-offload uses.
type RepoInfo struct {
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	Visibility    string `json:"visibility"`
}

// Info fetches the repository's metadata.
func (c *Client) Info(ctx context.Context) (*RepoInfo, error) {
	var info RepoInfo
	if err := c.api(ctx, "GET", "repos/"+c.Repo, nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// CheckWorkflow makes sure the workflow exists on the default branch and is
// enabled.
func (c *Client) CheckWorkflow(ctx context.Context, file string) error {
	var wf struct {
		State string `json:"state"`
	}
	err := c.api(ctx, "GET", c.path("actions/workflows/"+file), nil, &wf)
	if IsStatus(err, 404) {
		return fmt.Errorf("%s has no %s workflow on its default branch yet.\nRun `gh offload init`, merge the pull request it opens, then try again", c.Repo, file)
	}
	if err != nil {
		return err
	}
	if wf.State != "active" {
		return fmt.Errorf("the %s workflow is %s; enable it in the repository's Actions tab", file, strings.ReplaceAll(wf.State, "_", " "))
	}
	return nil
}

// Dispatch starts the workflow on ref and returns the new run's ID, or 0 when
// the server does not report it.
func (c *Client) Dispatch(ctx context.Context, file, ref string, inputs map[string]string) (int64, error) {
	body := map[string]any{"ref": ref, "inputs": inputs, "return_run_details": true}
	var resp struct {
		WorkflowRunID int64 `json:"workflow_run_id"`
	}
	err := c.api(ctx, "POST", c.path("actions/workflows/"+file+"/dispatches"), body, &resp)
	return resp.WorkflowRunID, err
}

// Run is the part of a workflow run gh-offload follows.
type Run struct {
	ID           int64  `json:"id"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	HTMLURL      string `json:"html_url"`
	DisplayTitle string `json:"display_title"`
}

// FindRun looks for the dispatched run by its title, for servers that do not
// return the run ID from a dispatch.
func (c *Client) FindRun(ctx context.Context, file, branch, title string, since time.Time) (int64, error) {
	q := url.Values{
		"event":    {"workflow_dispatch"},
		"branch":   {branch},
		"per_page": {"30"},
		"created":  {">=" + since.Add(-time.Minute).UTC().Format(time.RFC3339)},
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		var resp struct {
			Runs []Run `json:"workflow_runs"`
		}
		if err := c.api(ctx, "GET", c.path("actions/workflows/"+file+"/runs?"+q.Encode()), nil, &resp); err != nil {
			return 0, err
		}
		for _, r := range resp.Runs {
			if r.DisplayTitle == title {
				return r.ID, nil
			}
		}
		if time.Now().After(deadline) {
			return 0, errors.New("dispatched the workflow but could not find its run")
		}
		if err := sleep(ctx, 2*time.Second); err != nil {
			return 0, err
		}
	}
}

// Run fetches a workflow run.
func (c *Client) Run(ctx context.Context, id int64) (*Run, error) {
	var r Run
	if err := c.api(ctx, "GET", c.path("actions/runs/"+strconv.FormatInt(id, 10)), nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Step is one step of a job.
type Step struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// Job is one job of a run.
type Job struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Steps  []Step `json:"steps"`
}

// Jobs lists a run's jobs and their steps.
func (c *Client) Jobs(ctx context.Context, runID int64) ([]Job, error) {
	var resp struct {
		Jobs []Job `json:"jobs"`
	}
	err := c.api(ctx, "GET", c.path("actions/runs/"+strconv.FormatInt(runID, 10)+"/jobs"), nil, &resp)
	return resp.Jobs, err
}

// RunURL is the web page for a run.
func (c *Client) RunURL(runID int64) string {
	return "https://" + c.Host + "/" + c.Repo + "/actions/runs/" + strconv.FormatInt(runID, 10)
}

// CancelRun asks GitHub to cancel a run.
func (c *Client) CancelRun(ctx context.Context, runID int64) error {
	return c.api(ctx, "POST", c.path("actions/runs/"+strconv.FormatInt(runID, 10)+"/cancel"), nil, nil)
}

// DeleteBranch deletes a branch.
func (c *Client) DeleteBranch(ctx context.Context, branch string) error {
	return c.api(ctx, "DELETE", c.path("git/refs/heads/"+branch), nil, nil)
}

// DownloadArtifact downloads and unzips one artifact of a run into dir. It
// retries for a short while, since an artifact can appear a moment after its
// run finishes.
func (c *Client) DownloadArtifact(ctx context.Context, runID int64, name, dir string) error {
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			if err := sleep(ctx, 3*time.Second); err != nil {
				return err
			}
		}
		_, err = c.gh(ctx, nil, "run", "download", strconv.FormatInt(runID, 10), "-R", c.RepoArg(), "-n", name, "-D", dir)
		if err == nil {
			return nil
		}
	}
	return err
}

// FailedLog returns the log of a run's failed steps.
func (c *Client) FailedLog(ctx context.Context, runID int64) ([]byte, error) {
	return c.gh(ctx, nil, "run", "view", strconv.FormatInt(runID, 10), "-R", c.RepoArg(), "--log-failed")
}

// CreatePR opens a pull request and returns its URL.
func (c *Client) CreatePR(ctx context.Context, head, base, title, body string) (string, error) {
	out, err := c.gh(ctx, nil, "pr", "create", "-R", c.RepoArg(), "--head", head, "--base", base, "--title", title, "--body", body)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
