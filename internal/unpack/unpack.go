// Package unpack extracts the --get files a run sends back, refusing anything
// that would land outside the repository.
package unpack

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Result lists what Extract wrote and what it left out.
type Result struct {
	Files   []string // regular files written, relative to root
	Skipped []string // symlinks and special files, which are never written
}

// Extract unpacks a gzipped tar archive into root, keeping file modes.
func Extract(r io.Reader, root string) (*Result, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	res := &Result{}
	written := map[string]bool{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return res, nil
		}
		if err != nil {
			return res, err
		}
		name, ok := clean(hdr.Name)
		if !ok {
			return res, fmt.Errorf("refusing a path outside the repository: %q", hdr.Name)
		}
		if name == "." {
			continue
		}
		target := filepath.Join(root, filepath.FromSlash(name))

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return res, err
			}
		case tar.TypeReg:
			if err := writeFile(target, tr, os.FileMode(hdr.Mode).Perm()); err != nil {
				return res, err
			}
			res.add(name, written)
		case tar.TypeLink:
			// tar stores a second path to the same file (cargo's
			// target/release/app and target/release/deps/app-<hash>, for
			// example) as a hard link to the first one.
			src, ok := clean(hdr.Linkname)
			if !ok || !written[src] {
				res.Skipped = append(res.Skipped, name)
				continue
			}
			if err := copyFile(filepath.Join(root, filepath.FromSlash(src)), target); err != nil {
				return res, err
			}
			res.add(name, written)
		default:
			res.Skipped = append(res.Skipped, name)
		}
	}
}

// add records a written file once, even when a glob matched it twice.
func (r *Result) add(name string, written map[string]bool) {
	if !written[name] {
		r.Files = append(r.Files, name)
	}
	written[name] = true
}

func clean(name string) (string, bool) {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "/") || (len(name) >= 2 && name[1] == ':') {
		return "", false
	}
	c := path.Clean(name)
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", false
	}
	return c, true
}

// writeFile writes through a temporary file and renames it into place, so a
// failed download never leaves a half-written file behind.
func writeFile(target string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".gh-offload-*")
	if err != nil {
		return err
	}
	_, err = io.Copy(tmp, r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), mode|0o600)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), target)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeFile(dst, in, info.Mode().Perm())
}
