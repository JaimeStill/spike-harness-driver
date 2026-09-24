package session_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"uuid"

	"github.com/JaimeStill/spike-harness-driver/clutch/domain/session"
	"github.com/JaimeStill/spike-harness-driver/clutch/output"
	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/filestore"
	"github.com/JaimeStill/spike-harness-driver/internal/harnesstest"
)

// longPrompt streams until it is cancelled.
const longPrompt = "long"

func newService(opened *harness.Options) *session.Service {
	d := harnesstest.Driver{Stream: longPrompt}
	if opened != nil {
		d.Opened = func(opts harness.Options) { *opened = opts }
	}
	return session.New(
		func() (harness.Driver, error) { return d, nil },
		func() harness.Options { return harness.Options{Provider: "p", Model: "m"} },
	)
}

func TestRunToTheEnd(t *testing.T) {
	var opened harness.Options
	svc := newService(&opened)
	sess, err := svc.Open(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if opened.Provider != "p" || opened.Model != "m" {
		t.Errorf("opened with %+v", opened)
	}

	var kinds []harness.EventKind
	sent := 0
	o, err := svc.Run(t.Context(), sess, session.Exchange{Prompt: "hi"}, session.Observer{
		Sent:  func(uuid.UUID) { sent++ },
		Event: func(ev harness.Event) { kinds = append(kinds, ev.Kind) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Errorf("Sent called %d times", sent)
	}
	if o.Result.StopReason != "stop" || o.Result.Text != harnesstest.Reply || o.Err != nil {
		t.Errorf("outcome = %+v", o)
	}
	if len(kinds) != 7 || kinds[len(kinds)-1] != harness.EventEnded {
		t.Errorf("events = %v", kinds)
	}
}

func TestRunCancelsAfterDeltas(t *testing.T) {
	svc := newService(nil)
	sess, err := svc.Open(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	cancelledAt := 0
	o, err := svc.Run(t.Context(), sess, session.Exchange{Prompt: longPrompt, CancelAfter: 3}, session.Observer{
		Cancelling: func(n int) { cancelledAt = n },
	})
	if err != nil {
		t.Fatal(err)
	}
	if cancelledAt != 3 {
		t.Errorf("cancelled after %d deltas, want 3", cancelledAt)
	}
	if o.Result.StopReason != "aborted" || o.Err != nil {
		t.Errorf("outcome = %+v", o)
	}
}

func TestOpenFailsWithTheDriverError(t *testing.T) {
	boom := errors.New("no such harness")
	svc := session.New(func() (harness.Driver, error) { return nil, boom }, func() harness.Options { return harness.Options{} })
	if _, err := svc.Open(t.Context(), ""); !errors.Is(err, boom) {
		t.Errorf("Open = %v, want %v", err, boom)
	}
}

func TestOpenWithAddsTheSetup(t *testing.T) {
	base := harness.Options{
		Tools:        []harness.Tool{{Name: "fingerprint"}},
		Skills:       []harness.Skill{{Name: "clutch-weather"}},
		HarnessTools: []string{"bash"},
	}
	// A spare capacity would let an in-place append leak one Open's setup into the next.
	base.Tools = slices.Grow(base.Tools, 4)
	var opened harness.Options
	svc := session.New(
		func() (harness.Driver, error) {
			return harnesstest.Driver{Opened: func(o harness.Options) { opened = o }}, nil
		},
		func() harness.Options { return base },
	)
	names := func(tools []harness.Tool) []string {
		var n []string
		for _, t := range tools {
			n = append(n, t.Name)
		}
		return n
	}

	sess, err := svc.OpenWith(t.Context(), "", session.Setup{
		Tools:        []harness.Tool{{Name: "lookup_code"}},
		Skills:       []harness.Skill{{Name: "clutch-motto"}},
		HarnessTools: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	if got := names(opened.Tools); !slices.Equal(got, []string{"fingerprint", "lookup_code"}) {
		t.Errorf("tools = %v", got)
	}
	if len(opened.Skills) != 2 || opened.Skills[0].Name != "clutch-weather" || opened.Skills[1].Name != "clutch-motto" {
		t.Errorf("skills = %+v", opened.Skills)
	}
	if opened.HarnessTools == nil || len(opened.HarnessTools) != 0 {
		t.Errorf("harness tools = %#v, want empty", opened.HarnessTools)
	}

	sess, err = svc.Open(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	if got := names(opened.Tools); !slices.Equal(got, []string{"fingerprint"}) {
		t.Errorf("a plain Open offers %v", got)
	}
	if !slices.Equal(opened.HarnessTools, []string{"bash"}) {
		t.Errorf("a plain Open enables %v", opened.HarnessTools)
	}
	if got := names(svc.Tools()); !slices.Equal(got, []string{"fingerprint"}) || len(svc.Skills()) != 1 {
		t.Errorf("Tools = %v, Skills = %+v", got, svc.Skills())
	}
}

func TestSendCommand(t *testing.T) {
	var out, errs bytes.Buffer
	cmd := session.Commands(newService(nil), output.New(&out, &errs, nil))
	cmd.SetArgs([]string{"send", "hi"})
	cmd.SetOut(&out)
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"session " + harnesstest.SessionID + "\n", `: "hi"` + "\n", "ended", "result: stop=stop"} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
}

func TestSendResumesAndExchangesLists(t *testing.T) {
	var opened harness.Options
	store := filestore.New(t.TempDir())
	svc := session.New(
		func() (harness.Driver, error) {
			return harnesstest.Driver{Opened: func(o harness.Options) { opened = o }}, nil
		},
		func() harness.Options { return harness.Options{Store: store} },
	)
	execute := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		cmd := session.Commands(svc, output.New(&out, &out, nil))
		cmd.SetArgs(args)
		if err := cmd.ExecuteContext(t.Context()); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out.String()
	}

	if got := execute("send", "hi"); !strings.Contains(got, "resume with: clutch session send --session "+harnesstest.SessionID) {
		t.Errorf("send output lacks the resume line:\n%s", got)
	}
	execute("send", "--session", "resumed", "again")
	if opened.SessionID != "resumed" {
		t.Errorf("resumed with SessionID %q", opened.SessionID)
	}

	for _, verify := range []string{"", "--verify"} {
		args := []string{"exchanges", harnesstest.SessionID}
		if verify != "" {
			args = append(args, verify)
		}
		got := execute(args...)
		if strings.Count(got, "exchange ") != 1 || !strings.Contains(got, `prompt: "hi"`) {
			t.Errorf("%v:\n%s", args, got)
		}
		// harnesstest keeps no journal, so there is nothing to verify against.
		if (verify != "") != strings.Contains(got, "not verified: the harness keeps no journal") {
			t.Errorf("%v: verification line:\n%s", args, got)
		}
	}
	if got := execute("exchanges", "unknown"); !strings.Contains(got, "no exchanges recorded") {
		t.Errorf("exchanges of an unknown session:\n%s", got)
	}
}

func TestSendSchema(t *testing.T) {
	const schema = `{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`
	file := filepath.Join(t.TempDir(), "schema.json")
	array := filepath.Join(t.TempDir(), "array.json")
	for path, content := range map[string]string{file: schema + "\n", array: `["type"]`} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, value, err string
	}{
		{name: "inline", value: schema},
		{name: "file", value: file},
		{name: "invalid JSON", value: `{"type":`, err: "--schema: harness: invalid schema: not a JSON object"},
		{name: "not an object", value: array, err: "--schema: harness: invalid schema: not a JSON object"},
		{name: "not an object schema", value: `{"type":"array"}`, err: `--schema: harness: invalid schema: its type is "array", want "object"`},
		{name: "no type", value: `{}`, err: "--schema: harness: invalid schema: it declares no type"},
		{name: "missing file", value: filepath.Join(t.TempDir(), "none.json"), err: "--schema: open"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opened := false
			store := filestore.New(t.TempDir())
			svc := session.New(
				func() (harness.Driver, error) {
					return harnesstest.Driver{Opened: func(harness.Options) { opened = true }}, nil
				},
				func() harness.Options { return harness.Options{Store: store} },
			)
			var out bytes.Buffer
			cmd := session.Commands(svc, output.New(&out, &out, nil))
			cmd.SetArgs([]string{"send", "--schema", tc.value, "hi"})
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			err := cmd.ExecuteContext(t.Context())
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("send = %v, want an error containing %q", err, tc.err)
				}
				if opened {
					t.Error("a harness started for a bad schema")
				}
				return
			}
			// The stub harness gives no structured response, so the exchange ends in
			// ErrNoStructuredResponse, which shows the schema reached the harness.
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), harness.ErrNoStructuredResponse.Error()) {
				t.Errorf("output lacks the unanswered schema:\n%s", out.String())
			}
			recs, err := store.Records(t.Context(), harnesstest.SessionID)
			if err != nil || len(recs) != 1 || string(recs[0].Request.Schema) != schema {
				t.Errorf("recorded %+v, %v", recs, err)
			}
		})
	}
}
