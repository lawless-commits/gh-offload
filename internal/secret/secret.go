// Package secret spots files that look like credentials, so a snapshot does
// not upload them by accident.
package secret

import (
	"path"
	"strings"
)

var names = map[string]bool{
	".env":                 true,
	".secrets":             true,
	".netrc":               true,
	"_netrc":               true,
	".pgpass":              true,
	".htpasswd":            true,
	".git-credentials":     true,
	".pypirc":              true,
	"credentials":          true,
	"credentials.json":     true,
	"secrets.json":         true,
	"secrets.yml":          true,
	"secrets.yaml":         true,
	"secrets.toml":         true,
	"service-account.json": true,
}

var extensions = []string{".pem", ".key", ".p12", ".pfx", ".jks", ".keystore", ".kdbx", ".ppk", ".tfstate"}

var sshKeys = []string{"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519"}

// .env.<suffix> files with these suffixes are templates, not secrets.
var envTemplates = map[string]bool{"example": true, "sample": true, "template": true, "dist": true}

// Looks reports whether a repository path looks like it holds a secret.
func Looks(p string) bool {
	p = strings.ReplaceAll(p, "\\", "/")
	parts := strings.Split(p, "/")
	for _, dir := range parts[:len(parts)-1] {
		if dir == ".secrets" {
			return true
		}
	}
	base := strings.ToLower(parts[len(parts)-1])
	if names[base] {
		return true
	}
	if suffix, ok := strings.CutPrefix(base, ".env."); ok {
		return !envTemplates[suffix]
	}
	if strings.HasSuffix(base, ".pub") {
		return false
	}
	for _, k := range sshKeys {
		if strings.HasPrefix(base, k) {
			return true
		}
	}
	for _, ext := range extensions {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	return false
}

// Allowed reports whether one of the --include values covers p: the same path,
// a directory above it, or a glob that matches it.
func Allowed(p string, include []string) bool {
	for _, inc := range include {
		inc = strings.TrimSuffix(strings.TrimPrefix(strings.ReplaceAll(inc, "\\", "/"), "./"), "/")
		if inc == p || strings.HasPrefix(p, inc+"/") {
			return true
		}
		if ok, _ := path.Match(inc, p); ok {
			return true
		}
	}
	return false
}
