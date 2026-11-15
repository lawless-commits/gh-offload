package github

import (
	"errors"
	"fmt"
	"testing"
)

func TestParseRemote(t *testing.T) {
	tests := []struct {
		in, host, repo string
	}{
		{"https://github.com/octo/hello.git", "github.com", "octo/hello"},
		{"https://github.com/octo/hello", "github.com", "octo/hello"},
		{"https://user:token@github.com/octo/hello.git", "github.com", "octo/hello"},
		{"git@github.com:octo/hello.git", "github.com", "octo/hello"},
		{"git@github.com:octo/hello", "github.com", "octo/hello"},
		{"ssh://git@github.com/octo/hello.git", "github.com", "octo/hello"},
		{"ssh://git@ssh.github.com:443/octo/hello.git", "github.com", "octo/hello"},
		{"https://GHE.example.com/team/app.git/", "ghe.example.com", "team/app"},
		{"git@ghe.example.com:team/app.git", "ghe.example.com", "team/app"},
	}
	for _, tt := range tests {
		host, repo, err := ParseRemote(tt.in)
		if err != nil {
			t.Errorf("ParseRemote(%q): %v", tt.in, err)
			continue
		}
		if host != tt.host || repo != tt.repo {
			t.Errorf("ParseRemote(%q) = %q, %q; want %q, %q", tt.in, host, repo, tt.host, tt.repo)
		}
	}
	for _, bad := range []string{"", "/srv/git/repo.git", "../repo", "https://github.com/onlyowner", "https://github.com/a/b/c"} {
		if _, _, err := ParseRemote(bad); err == nil {
			t.Errorf("ParseRemote(%q) succeeded, want an error", bad)
		}
	}
}

func TestRepoArg(t *testing.T) {
	if got := (&Client{Host: "github.com", Repo: "o/r"}).RepoArg(); got != "o/r" {
		t.Errorf("github.com RepoArg = %q", got)
	}
	if got := (&Client{Host: "ghe.example.com", Repo: "o/r"}).RepoArg(); got != "ghe.example.com/o/r" {
		t.Errorf("enterprise RepoArg = %q", got)
	}
}

func TestIsStatus(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &HTTPError{Status: 404, Message: "gh: Not Found (HTTP 404)"})
	if !IsStatus(err, 404) {
		t.Error("IsStatus(404) = false for a wrapped 404")
	}
	if IsStatus(err, 422) || IsStatus(errors.New("plain"), 404) {
		t.Error("IsStatus matched the wrong error")
	}
	m := httpStatus.FindStringSubmatch("gh: Unexpected inputs provided: [\"color\"] (HTTP 422)")
	if m == nil || m[1] != "422" {
		t.Errorf("status regexp did not find 422: %v", m)
	}
}
