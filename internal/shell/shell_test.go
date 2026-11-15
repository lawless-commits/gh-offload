package shell

import "testing"

func TestQuote(t *testing.T) {
	tests := map[string]string{
		"":               "''",
		"cargo":          "cargo",
		"--release":      "--release",
		"a/b.c:d=e,f@g%": "a/b.c:d=e,f@g%",
		"my file":        "'my file'",
		"it's":           `'it'"'"'s'`,
		"$HOME":          "'$HOME'",
		"a;b":            "'a;b'",
		"*.txt":          "'*.txt'",
	}
	for in, want := range tests {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestJoin(t *testing.T) {
	if got := Join([]string{"make && make test"}); got != "make && make test" {
		t.Errorf("single argument changed: %s", got)
	}
	if got := Join([]string{"cargo", "build", "--release"}); got != "cargo build --release" {
		t.Errorf("Join = %s", got)
	}
	if got := Join([]string{"ls", "my file", "it's"}); got != `ls 'my file' 'it'"'"'s'` {
		t.Errorf("Join = %s", got)
	}
}
