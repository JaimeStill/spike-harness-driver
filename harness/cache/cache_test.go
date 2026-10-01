package cache_test

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/JaimeStill/spike-harness-driver/harness/cache"
)

func TestFSWritesOnceUnderItsDigest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	fsys := fstest.MapFS{"SKILL.md": {Data: []byte("# motto")}, "ref/notes.md": {Data: []byte("notes")}}

	dir, err := cache.FS(root, "motto", fsys)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "ref", "notes.md")); err != nil || string(data) != "notes" {
		t.Fatalf("cached file = %q, %v", data, err)
	}
	again, err := cache.FS(root, "motto", fsys)
	if err != nil || again != dir {
		t.Fatalf("same content = %s, %v; want the same directory %s", again, err, dir)
	}

	fsys["SKILL.md"] = &fstest.MapFile{Data: []byte("# changed")}
	changed, err := cache.FS(root, "motto", fsys)
	if err != nil || changed == dir {
		t.Fatalf("changed content = %s, %v; want a new directory", changed, err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the earlier directory is gone: %v", err)
	}
}

func TestADamagedEntryIsWrittenAgain(t *testing.T) {
	root := t.TempDir()
	data := []byte("export default {}")
	write := func(d string) error { return os.WriteFile(filepath.Join(d, "x.ts"), data, 0o600) }
	dir, err := cache.Dir(root, "x", cache.FileDigest("x.ts", data), write)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.ts"), []byte("planted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Dir(root, "x", cache.FileDigest("x.ts", data), write); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "x.ts")); string(got) != string(data) {
		t.Fatalf("entry = %q, want it written again", got)
	}
}
