package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/JaimeStill/spike-harness-driver/clutch/output"
	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/workflow"
)

// Commands builds the workflow command family over svc, rendering through out.
func Commands(svc *Service, out *output.Output) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workflow",
		Short: "Run workflows: DAGs of exchanges over several harness sessions",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(runCommand(svc, out), resumeCommand(svc, out), cancelCommand(svc, out), listCommand(svc, out), showCommand(svc, out))
	return cmd
}

// runCommand builds "workflow run", which starts a run and follows it to its end.
func runCommand(svc *Service, out *output.Output) *cobra.Command {
	var (
		limit int
		input string
	)
	cmd := &cobra.Command{
		Use:   "run <workflow.json>",
		Short: "Start a run of a workflow and follow it to its end",
		Long: "run starts a run of the workflow file and prints its events as they happen: each\n" +
			"session as it opens, each step as it starts and ends, with the step's result, and\n" +
			"the tool calls and errors of its exchange. Steps whose dependencies have ended run\n" +
			"concurrently, each session carrying one step at a time, and --limit bounds the\n" +
			"exchanges in flight. A session runs on the harness, provider, and model it names,\n" +
			"and on the flags' where it names none.\n\n" +
			"An interrupt stops the run without ending it: its exchanges in flight are cancelled\n" +
			"and its sessions closed, and \"workflow resume\" takes it up, running only the steps\n" +
			"that hadn't ended.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w, err := Load(args[0])
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("input") {
				w.Input = input
			}
			r, err := svc.Runner(limit)
			if err != nil {
				return err
			}
			id, err := r.Start(cmd.Context(), w)
			if err != nil {
				return err
			}
			out.Printf("run %s: %s, %d steps over %d sessions", id, w.Name, len(w.Steps), len(w.Sessions))
			return follow(cmd.Context(), r, id, 0, out)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "the most exchanges in flight at once (0 is no limit)")
	cmd.Flags().StringVar(&input, "input", "", "replace the workflow's input, which prompts reach as {{.Input}}")
	return cmd
}

// resumeCommand builds "workflow resume", which takes up an unfinished run.
func resumeCommand(svc *Service, out *output.Output) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "resume <run-id>",
		Short: "Take up a run a previous process left unfinished, and follow it to its end",
		Long: "resume takes up a run whose log hasn't ended. Its finished steps keep their results,\n" +
			"each session reopens as the harness session the log recorded, and a step whose\n" +
			"exchange ended after the log last heard of it is adopted from the session's exchange\n" +
			"records rather than run again. The run's events print from where the log stood.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := svc.Runner(limit)
			if err != nil {
				return err
			}
			s, err := r.State(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := r.Resume(cmd.Context(), args[0]); err != nil {
				return err
			}
			out.Printf("run %s: %s, resumed with %d of %d steps done", s.RunID, s.Workflow.Name, s.Done(), len(s.Workflow.Steps))
			return follow(cmd.Context(), r, args[0], s.Seq, out)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "the most exchanges in flight at once (0 is no limit)")
	return cmd
}

// cancelCommand builds "workflow cancel", which ends an unfinished run.
func cancelCommand(svc *Service, out *output.Output) *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <run-id>",
		Short: "End an unfinished run as cancelled, so nothing resumes it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := svc.Runner(0)
			if err != nil {
				return err
			}
			if err := r.Cancel(cmd.Context(), args[0]); err != nil {
				return err
			}
			out.Printf("run %s cancelled", args[0])
			return nil
		},
	}
}

// listCommand builds "workflow list".
func listCommand(svc *Service, out *output.Output) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the runs in the log, oldest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := svc.Runner(0)
			if err != nil {
				return err
			}
			states, err := r.Runs(cmd.Context())
			if err != nil {
				return err
			}
			if len(states) == 0 {
				out.Printf("no runs")
			}
			for _, s := range states {
				out.Printf("%s  %-9s %d/%d  %s  %s", s.RunID, s.Status, s.Done(), len(s.Workflow.Steps), s.Started.Local().Format(time.DateTime), s.Workflow.Name)
			}
			return nil
		},
	}
}

// showCommand builds "workflow show", which prints a run's state as JSON.
func showCommand(svc *Service, out *output.Output) *cobra.Command {
	return &cobra.Command{
		Use:   "show <run-id>",
		Short: "Print a run's state, as its log has it, as JSON",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := svc.Runner(0)
			if err != nil {
				return err
			}
			s, err := r.State(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			b, err := json.MarshalIndent(s, "", "  ")
			if err != nil {
				return err
			}
			out.Printf("%s", b)
			return nil
		},
	}
}

// follow prints the run's events after Seq after until the run ends or ctx does, then how it
// stands. A run that didn't end done is an error, so the exit code says how it went.
func follow(ctx context.Context, r *workflow.Runner, id string, after int, out *output.Output) error {
	s, err := Follow(ctx, r, id, after, func(e workflow.Event) {
		if line := EventLine(e); line != "" {
			out.Printf("%s", line)
		}
	})
	if errors.Is(err, context.Canceled) {
		out.Printf("-- interrupted with %d of %d steps done; resume with: clutch workflow resume %s", s.Done(), len(s.Workflow.Steps), id)
		return err
	}
	if err != nil {
		return err
	}
	out.Printf("run %s %s: %d of %d steps done in %s", id, s.Status, s.Done(), len(s.Workflow.Steps), s.Ended.Sub(s.Started).Round(time.Millisecond))
	if s.Status != workflow.StatusDone {
		return fmt.Errorf("run %s %s", id, s.Status)
	}
	return nil
}

// resultWidth bounds a result's text on a step_ended line.
const resultWidth = 160

// EventLine formats a run event as one line: the time, the logged event's number, its kind,
// and its detail. Of an exchange's live events, it shows tool calls and results and errors,
// and returns "" for the rest, such as text deltas: several steps stream at once, and the
// step's result carries its text.
func EventLine(e workflow.Event) string {
	head := fmt.Sprintf("%s #%-3d %-15s", e.Time.Local().Format("15:04:05.000"), e.Seq, e.Kind)
	switch e.Kind {
	case workflow.KindRunStarted:
		return fmt.Sprintf("%s %s", head, e.Workflow.Name)
	case workflow.KindSessionOpened:
		return fmt.Sprintf("%s %s = harness session %s", head, e.Session, e.SessionID)
	case workflow.KindStepStarted:
		return fmt.Sprintf("%s %s on %s, exchange %s", head, e.Step, e.Session, output.Short(e.ExchangeID.String()))
	case workflow.KindStepEnded:
		line := fmt.Sprintf("%s %s %s", head, e.Step, e.Status)
		if e.Adopted {
			line += " (adopted from the session's records)"
		}
		if e.Err != "" {
			line += ": " + e.Err
		}
		if e.Result != nil {
			line += ": " + summary(*e.Result)
		}
		return line
	case workflow.KindRunPaused:
		return fmt.Sprintf("%s until %s: %s", head, e.Until.Local().Format(time.TimeOnly), e.Err)
	case workflow.KindRunEnded:
		if e.Err != "" {
			return fmt.Sprintf("%s %s: %s", head, e.Status, e.Err)
		}
		return fmt.Sprintf("%s %s", head, e.Status)
	case workflow.KindExchange:
		x := e.Exchange
		switch x.Kind {
		case harness.EventToolCall, harness.EventToolResult:
			return fmt.Sprintf("%s     %s %s %s", e.Time.Local().Format("15:04:05.000"), e.Step, x.Kind, x.Tool)
		case harness.EventError, harness.EventStructuredRejected:
			return fmt.Sprintf("%s     %s %s %s", e.Time.Local().Format("15:04:05.000"), e.Step, x.Kind, x.Err)
		}
		return ""
	}
	return head
}

// summary is a result as one cut line: the structured response when there is one, else the
// text.
func summary(res harness.Result) string {
	s := res.Text
	if len(res.Structured) > 0 {
		s = string(res.Structured)
	}
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > resultWidth {
		s = string(r[:resultWidth]) + "…"
	}
	return s
}
