package session_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"uuid"

	"github.com/JaimeStill/spike-harness-driver/domain/session"
	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/internal/harnesstest"
	"github.com/JaimeStill/spike-harness-driver/output"
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
	sess, err := svc.Open(t.Context())
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
	sess, err := svc.Open(t.Context())
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
	if _, err := svc.Open(t.Context()); !errors.Is(err, boom) {
		t.Errorf("Open = %v, want %v", err, boom)
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
