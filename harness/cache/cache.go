// Package cache keeps the files a driver loads into a harness from outside it, such as an
// extension, a plugin, or a skill that exists only as an fs.FS, in a directory the harness
// reads them from.
//
// Each entry is written once, under a name its content decides, so sessions share it, and a session
// resumed in a new process still finds the files its history names: changed content gets a new
// directory, and no session's files change under it. The harness runs or follows what the cache
// holds, so the root must belong to the current user and be writable by no one else.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io/fs"
	"os"
	"path/filepath"
)

// FS returns the directory under root that holds fsys's files, named by name and a digest of
// every file's path and content.
func FS(root, name string, fsys fs.FS) (string, error) {
	digest, err := TreeDigest(fsys)
	if err != nil {
		return "", err
	}
	return Dir(root, name, digest, func(dir string) error { return os.CopyFS(dir, fsys) })
}

// Dir returns the directory root/name-digest, which write fills the first time. It writes to a
// temporary name it then renames, so a concurrent writer of the same content never sees the
// directory half written.
//
// A driver runs what the cache holds, as Pi loads its bridge as code, so anyone else able to write
// under root could plant a directory under a name the driver would trust. Dir therefore requires
// root to belong to the current user and be writable by no one else, and uses a directory found
// under the right name only when its content still has the digest it is named by.
func Dir(root, name, digest string, write func(dir string) error) (string, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	if err := Private(root); err != nil {
		return "", err
	}
	dir := filepath.Join(root, name+"-"+digest)
	if _, err := os.Stat(dir); err == nil {
		if got, err := TreeDigest(os.DirFS(dir)); err == nil && got == digest {
			return dir, nil
		}
		// Only this user writes under root, so a mismatch is damage, such as a copy a crash
		// cut short: write the directory again.
		if err := os.RemoveAll(dir); err != nil {
			return "", err
		}
	}
	tmp, err := os.MkdirTemp(root, ".write-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	staged := filepath.Join(tmp, "d")
	if err := os.Mkdir(staged, 0o700); err != nil {
		return "", err
	}
	if err := write(staged); err != nil {
		return "", err
	}
	// Losing the race to another writer of the same content leaves the directory it wrote.
	if err := os.Rename(staged, dir); err != nil {
		if _, statErr := os.Stat(dir); statErr != nil {
			return "", err
		}
	}
	return dir, nil
}

// TreeDigest is the first 12 hex digits of a SHA-256 over every regular file's path and
// content, in the lexical order fs.WalkDir walks.
func TreeDigest(fsys fs.FS) (string, error) {
	h := sha256.New()
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		hashFile(h, p, data)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:12], nil
}

// FileDigest is TreeDigest of a tree that holds data alone, at path p.
func FileDigest(p string, data []byte) string {
	h := sha256.New()
	hashFile(h, p, data)
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// hashFile adds one file to a tree's digest, length-prefixed, so no two trees hash alike by
// moving bytes between files.
func hashFile(h hash.Hash, p string, data []byte) {
	_, _ = fmt.Fprintf(h, "%s\x00%d\x00", p, len(data))
	_, _ = h.Write(data)
}
