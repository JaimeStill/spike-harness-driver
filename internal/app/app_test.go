package app_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/internal/app"
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
	for _, name := range []string{"exchange", "cancel", "needs the harness executable"} {
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

func TestScenarioChecksItsNeeds(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	code, _, errs := execute(t, "scenario", "exchange")
	if code != 1 || !strings.Contains(errs, "need the harness executable on the PATH") {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
}
