package unpack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

type entry struct {
	hdr  tar.Header
	body string
}

func archive(t *testing.T, entries ...entry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := e.hdr
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestExtract(t *testing.T) {
	root := t.TempDir()
	// An existing file is replaced.
	if err := os.MkdirAll(filepath.Join(root, "target", "release"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "target", "release", "app"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	buf := archive(t,
		entry{hdr: tar.Header{Name: "target/", Typeflag: tar.TypeDir, Mode: 0o755}},
		entry{hdr: tar.Header{Name: "target/release/deps/app-1234", Typeflag: tar.TypeReg, Mode: 0o755}, body: "binary"},
		entry{hdr: tar.Header{Name: "target/release/app", Typeflag: tar.TypeLink, Linkname: "target/release/deps/app-1234"}},
		entry{hdr: tar.Header{Name: "./out/report.txt", Typeflag: tar.TypeReg, Mode: 0o644}, body: "report"},
		entry{hdr: tar.Header{Name: "out/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}},
		entry{hdr: tar.Header{Name: "target/release/app", Typeflag: tar.TypeLink, Linkname: "target/release/deps/app-1234"}},
	)
	res, err := Extract(buf, root)
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{"target/release/deps/app-1234", "target/release/app", "out/report.txt"}
	if !reflect.DeepEqual(res.Files, wantFiles) {
		t.Errorf("Files = %v, want %v", res.Files, wantFiles)
	}
	if !reflect.DeepEqual(res.Skipped, []string{"out/link"}) {
		t.Errorf("Skipped = %v", res.Skipped)
	}
	got, err := os.ReadFile(filepath.Join(root, "target", "release", "app"))
	if err != nil || string(got) != "binary" {
		t.Errorf("hard-linked file = %q, %v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "out", "link")); !os.IsNotExist(err) {
		t.Error("a symlink was written")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(root, "target", "release", "app"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o100 == 0 {
			t.Errorf("executable bit lost: %v", info.Mode())
		}
	}
	leftovers, _ := filepath.Glob(filepath.Join(root, "target", "release", ".gh-offload-*"))
	if len(leftovers) > 0 {
		t.Errorf("temporary files left behind: %v", leftovers)
	}
}

func TestExtractRefusesEscapes(t *testing.T) {
	for _, name := range []string{"../evil", "a/../../evil", "/etc/evil", `C:\evil`, `..\evil`} {
		root := t.TempDir()
		buf := archive(t, entry{hdr: tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644}, body: "x"})
		if _, err := Extract(buf, root); err == nil {
			t.Errorf("Extract accepted %q", name)
		}
	}
}
