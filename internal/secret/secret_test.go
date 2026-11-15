package secret

import "testing"

func TestLooks(t *testing.T) {
	secrets := []string{
		".env", "app/.env", ".env.local", ".env.production", ".secrets", ".secrets/token",
		"deploy/key.pem", "tls.KEY", "id_rsa", "home/.ssh/id_ed25519", "id_ed25519_github",
		"store.p12", "creds/credentials.json", "terraform.tfstate", ".netrc", "config/secrets.yml",
	}
	for _, p := range secrets {
		if !Looks(p) {
			t.Errorf("Looks(%q) = false, want true", p)
		}
	}
	safe := []string{
		".env.example", ".env.sample", "README.md", "src/keyboard.go", "id_rsa.pub",
		"monkey", "docs/secrets.md", "environment.ts", "key.rs", "src/secret/secret.go",
	}
	for _, p := range safe {
		if Looks(p) {
			t.Errorf("Looks(%q) = true, want false", p)
		}
	}
}

func TestAllowed(t *testing.T) {
	inc := []string{".env.local", "./certs/", "*.pem"}
	for _, p := range []string{".env.local", "certs/dev.key", "dev.pem"} {
		if !Allowed(p, inc) {
			t.Errorf("Allowed(%q) = false", p)
		}
	}
	for _, p := range []string{".env", "certsx/dev.key", "sub/dev.pem"} {
		if Allowed(p, inc) {
			t.Errorf("Allowed(%q) = true", p)
		}
	}
}
