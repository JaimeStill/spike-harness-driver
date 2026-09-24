package catalog_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/catalog"
)

// commandTools skips a test on Windows, where the test tools' shell scripts don't run.
func commandTools(t *testing.T) map[string]harness.Tool {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the test tools are POSIX shell scripts")
	}
	tools, err := catalog.Tools("testdata/tools")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	byName := map[string]harness.Tool{}
	for _, tool := range tools {
		names = append(names, tool.Name)
		byName[tool.Name] = tool
	}
	if got, want := strings.Join(names, ","), "echo,fail,leaves,slow"; got != want {
		t.Fatalf("names = %s, want %s", got, want)
	}
	return byName
}

func TestCommandToolEcho(t *testing.T) {
	tool := commandTools(t)["echo"]
	if got, want := string(tool.Schema), `{"type":"object","properties":{"text":{"type":"string"}}}`; got != want {
		t.Fatalf("schema = %s, want %s", got, want)
	}
	args := json.RawMessage(`{"text":"hello"}`)
	got, err := tool.Handler(t.Context(), args)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(args) {
		t.Fatalf("result = %q, want %q", got, args)
	}
}

func TestCommandToolFail(t *testing.T) {
	tool := commandTools(t)["fail"]
	_, err := tool.Handler(t.Context(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{`tool "fail"`, "exit status 3", "lookup failed: no such key"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to contain %q", err, want)
		}
	}
}

func TestCommandToolPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("cat isn't on Windows's PATH")
	}
	dir := t.TempDir()
	writeManifest(t, dir, `{"name":"cat","description":"Cats.","inputSchema":{"type":"object"},"command":["cat"]}`)
	tool, err := catalog.CommandTool(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := tool.Handler(t.Context(), json.RawMessage(`{"a":1}`))
	if err != nil || got != `{"a":1}` {
		t.Fatalf("result = %q, %v", got, err)
	}
}

func TestCommandToolInvalid(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		want     string
	}{
		{"missing inputSchema", `{"name":"t","description":"T.","command":["x"]}`, "inputSchema is missing"},
		{"non-object type", `{"name":"t","description":"T.","inputSchema":{"type":"array"},"command":["x"]}`, `its type is "array", want "object"`},
		{"schema not an object", `{"name":"t","description":"T.","inputSchema":[],"command":["x"]}`, "not a JSON object"},
		{"empty command", `{"name":"t","description":"T.","inputSchema":{"type":"object"},"command":[]}`, "command is empty"},
		{"empty description", `{"name":"t","description":" ","inputSchema":{"type":"object"},"command":["x"]}`, "description is empty"},
		{"bad name", `{"name":"a b","description":"T.","inputSchema":{"type":"object"},"command":["x"]}`, `tool "a b": the name isn't`},
		{"not JSON", `{`, "tool.json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeManifest(t, dir, c.manifest)
			_, err := catalog.CommandTool(dir)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestToolsDuplicate(t *testing.T) {
	root := t.TempDir()
	for _, sub := range []string{"one", "two"} {
		dir := filepath.Join(root, sub)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeManifest(t, dir, `{"name":"same","description":"S.","inputSchema":{"type":"object"},"command":["x"]}`)
	}
	_, err := catalog.Tools(root)
	if err == nil || !strings.Contains(err.Error(), `both name the tool "same"`) {
		t.Fatalf("err = %v, want a duplicate name", err)
	}
}

func writeManifest(t *testing.T, dir, manifest string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "tool.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}
