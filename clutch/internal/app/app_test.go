package app_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/clutch/internal/app"
)

func execute(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errs bytes.Buffer
	a := app.New(&out, &errs)
	a.SetArgs(args)
	return a.Run(t.Context()), out.String(), errs.String()
}

func TestListNamesEveryScenario(t *testing.T) {
	code, out, _ := execute(t, "list")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, name := range []string{"exchange", "cancel", "resume", "tool", "skill", "needs the harness executable"} {
		if !strings.Contains(out, name) {
			t.Errorf("list lacks %q:\n%s", name, out)
		}
	}
}

func TestRootPrintsHelpAndScenarios(t *testing.T) {
	code, out, _ := execute(t)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"session", "scenario", "--harness", "Scenarios:"} {
		if !strings.Contains(out, want) {
			t.Errorf("root output lacks %q:\n%s", want, out)
		}
	}
}

func TestUnknownHarnessFailsBeforeTheCommandRuns(t *testing.T) {
	code, out, errs := execute(t, "--harness", "nope", "list")
	if code != 1 || !strings.Contains(errs, `unknown harness "nope"`) {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
	if out != "" {
		t.Errorf("list ran anyway:\n%s", out)
	}
}

func TestSourcesThatDontLoadFailBeforeTheCommandRuns(t *testing.T) {
	empty := t.TempDir()
	badSkill := t.TempDir()
	if err := os.MkdirAll(filepath.Join(badSkill, "nameless"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badSkill, "nameless", "SKILL.md"), []byte("---\ndescription: d\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(empty, "missing")
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"--tools", missing}, "--tools " + missing + ": "},
		{[]string{"--skills", missing}, "--skills " + missing + ": "},
		{[]string{"--tools", empty}, "no command tools"},
		{[]string{"--skills", empty}, "no skills"},
		{[]string{"--skills", badSkill}, "frontmatter has no name"},
		{[]string{"--tools", "../../examples/tools", "--tools", "../../examples/tools"}, `tool "fingerprint" is also in`},
		// The skills root isn't a tools root: a mistyped flag fails rather than loading nothing.
		{[]string{"--tools", "../../examples/skills"}, "no command tools"},
	}
	for _, tt := range tests {
		code, out, errs := execute(t, append(tt.args, "list")...)
		if code != 1 || !strings.Contains(errs, tt.want) {
			t.Errorf("%v: exit %d, stderr %q, want %q", tt.args, code, errs, tt.want)
		}
		if out != "" {
			t.Errorf("%v: list ran anyway:\n%s", tt.args, out)
		}
	}
}

func TestTheExamplesLoad(t *testing.T) {
	code, _, errs := execute(t, "--tools", "../../examples/tools", "--skills", "../../examples/skills", "list")
	if code != 0 {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
}

func TestScenarioChecksItsNeeds(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	code, _, errs := execute(t, "scenario", "exchange")
	if code != 1 || !strings.Contains(errs, "need the harness executable on the PATH") {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
}

func TestExchangesReadTheStateDirectory(t *testing.T) {
	code, out, errs := execute(t, "--state", t.TempDir(), "session", "exchanges", "none")
	if code != 0 || !strings.Contains(out, "no exchanges recorded for session none") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errs)
	}
}
