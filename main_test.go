package main

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lawless-commits/gh-offload/internal/gitrepo"
)

func TestParseRunFlags(t *testing.T) {
	var stderr bytes.Buffer
	o, cmd, err := parseRunFlags([]string{"--os", "macos", "--get", "a", "--get", "b/*", "-y", "--", "cargo", "build", "--release"}, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if o.os != "macos" || !o.yes || !reflect.DeepEqual([]string(o.get), []string{"a", "b/*"}) {
		t.Errorf("options = %+v", o)
	}
	if !reflect.DeepEqual(cmd, []string{"cargo", "build", "--release"}) {
		t.Errorf("command = %v", cmd)
	}

	// Without --, flag parsing stops at the first non-flag argument.
	_, cmd, err = parseRunFlags([]string{"--json", "go", "test", "-run", "X"}, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cmd, []string{"go", "test", "-run", "X"}) {
		t.Errorf("command = %v", cmd)
	}
}

func TestParseRunFlagsErrors(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"--"},
		{"--timeout", "0", "--", "x"},
		{"--timeout", "361", "--", "x"},
		{"--get", "", "--", "x"},
		{"--nope", "--", "x"},
	} {
		var stderr bytes.Buffer
		_, _, err := parseRunFlags(args, &stderr)
		var ee *exitError
		if !errors.As(err, &ee) || ee.code != exitUsage {
			t.Errorf("parseRunFlags(%q) = %v, want a usage error", args, err)
		}
	}
}

func TestRunnerLabel(t *testing.T) {
	if got, _ := runnerLabel("linux-arm", ""); got != "ubuntu-24.04-arm" {
		t.Errorf("linux-arm = %q", got)
	}
	if got, _ := runnerLabel("linux", "macos-15"); got != "macos-15" {
		t.Errorf("--runner did not win: %q", got)
	}
	if _, err := runnerLabel("solaris", ""); err == nil || !strings.Contains(err.Error(), "linux-arm") {
		t.Errorf("unknown --os error = %v", err)
	}
}

func TestDescribe(t *testing.T) {
	snap := &gitrepo.Snapshot{Head: "0123456789abcdef", Changes: []gitrepo.Change{
		{Status: "M", Path: "a.go"}, {Status: "A", Path: "b.go"}, {Status: "A", Path: "c.go"}, {Status: "D", Path: "d.go"},
	}}
	want := "snapshot: HEAD (0123456) + 1 changed, 2 new, 1 deleted: a.go, b.go, c.go, d.go"
	if got := describe(snap); got != want {
		t.Errorf("describe = %q\nwant       %q", got, want)
	}
	if got := describe(&gitrepo.Snapshot{Head: "0123456789"}); got != "snapshot: HEAD (0123456), no local changes" {
		t.Errorf("clean describe = %q", got)
	}
}

func TestList(t *testing.T) {
	if got := list([]string{"a", "b", "c"}, 2); got != "a, b and 1 more" {
		t.Errorf("list = %q", got)
	}
}

func TestNewIDIsBranchSafe(t *testing.T) {
	id := newID()
	if len(id) != len("20060102-150405-abcdef") || strings.ContainsAny(id, " ~^:?*[\\") {
		t.Errorf("newID = %q", id)
	}
	if newID() == id {
		t.Error("two ids in a row are equal")
	}
}

func TestHelpAndVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := realMain([]string{"--help"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "gh offload init") {
		t.Errorf("--help: code %d, output %q", code, out.String())
	}
	out.Reset()
	if code := realMain([]string{"version"}, &out, &errb); code != 0 || !strings.HasPrefix(out.String(), "gh-offload ") {
		t.Errorf("version: code %d, output %q", code, out.String())
	}
	out.Reset()
	if code := realMain([]string{"init", "--print"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "workflow_dispatch") {
		t.Errorf("init --print: code %d", code)
	}
}
