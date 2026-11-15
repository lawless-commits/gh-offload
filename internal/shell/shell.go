// Package shell turns command-line arguments back into a bash command.
package shell

import (
	"regexp"
	"strings"
)

var safe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// Quote makes s a single bash word.
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	if safe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// Join builds the command to run. A single argument is used as-is, like ssh
// does, so `gh offload -- 'make && make test'` works; several arguments are
// quoted one by one, so `gh offload -- ls "my file"` keeps its spaces.
func Join(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = Quote(a)
	}
	return strings.Join(quoted, " ")
}
