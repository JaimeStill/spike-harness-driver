package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"uuid"

	"github.com/spf13/cobra"

	"github.com/JaimeStill/spike-harness-driver/clutch/output"
)

// Commands builds the session command family over svc, rendering through out.
func Commands(svc *Service, out *output.Output) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Open harness sessions and run exchanges on them",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(sendCommand(svc, out), exchangesCommand(svc, out))
	return cmd
}

// sendCommand builds "session send", which opens a session, runs one exchange, and closes it.
func sendCommand(svc *Service, out *output.Output) *cobra.Command {
	var (
		id          string
		cancelAfter int
		schema      string
	)
	cmd := &cobra.Command{
		Use:   "send <prompt>",
		Short: "Open or resume a session and run one exchange on it",
		Long: "send opens a session on the harness, sends the prompt as one exchange, prints every\n" +
			"normalized event tagged with its session and exchange IDs, and prints the result.\n" +
			"--session resumes that session instead of opening a new one; the harness scopes\n" +
			"session IDs to the working directory, so resume from the same directory.\n" +
			"--cancel-after cancels the exchange after that many text deltas.\n" +
			"--schema asks for a structured response: the model answers by calling a respond tool\n" +
			"whose arguments the harness validates against the schema, and the result prints them\n" +
			"as structured. The value is an object schema, inline JSON or a path to a JSON file.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := parseSchema(schema)
			if err != nil {
				return fmt.Errorf("--schema: %w", err)
			}
			return send(cmd.Context(), svc, out, id, Exchange{Prompt: args[0], Schema: s, CancelAfter: cancelAfter})
		},
	}
	cmd.Flags().StringVar(&id, "session", "", "resume the session with this ID")
	cmd.Flags().IntVar(&cancelAfter, "cancel-after", 0, "cancel after this many text deltas (0 never cancels)")
	cmd.Flags().StringVar(&schema, "schema", "", "ask for a structured response matching this object schema: inline JSON, or a path to a JSON file")
	return cmd
}

// parseSchema reads --schema's value: inline JSON when it starts with "{", otherwise the path
// of a JSON file. The schema must be an object schema, the only kind a structured response
// takes. An empty value asks for none.
func parseSchema(value string) (json.RawMessage, error) {
	if value == "" {
		return nil, nil
	}
	raw := []byte(value)
	if !strings.HasPrefix(strings.TrimSpace(value), "{") {
		b, err := os.ReadFile(value)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	raw = bytes.TrimSpace(raw)
	var s struct {
		Type json.RawMessage `json:"type"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("not a JSON object: %w", err)
	}
	switch t := string(bytes.TrimSpace(s.Type)); {
	case t == "":
		return nil, errors.New(`the schema declares no type, want "object"`)
	case t != `"object"`:
		return nil, fmt.Errorf(`the schema's type is %s, want "object"`, t)
	}
	return raw, nil
}

func send(ctx context.Context, svc *Service, out *output.Output, id string, e Exchange) (err error) {
	sess, err := svc.Open(ctx, id)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := sess.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close: %w", cerr))
		}
	}()
	out.Printf("session %s", sess.ID())
	o, err := svc.Run(ctx, sess, e, Observer{
		Sent:       func(id uuid.UUID) { out.Printf("exchange %s: %q", id, e.Prompt) },
		Event:      out.Event,
		Cancelling: func(n int) { out.Printf("-- cancelling after %d text deltas", n) },
	})
	if err != nil {
		return err
	}
	out.Result(o.Result, o.Err)
	out.Printf("resume with: clutch session send --session %s <prompt>", sess.ID())
	return nil
}

// exchangesCommand builds "session exchanges", which lists a session's recorded exchanges.
func exchangesCommand(svc *Service, out *output.Output) *cobra.Command {
	var verify bool
	cmd := &cobra.Command{
		Use:   "exchanges <session-id>",
		Short: "List a session's recorded exchanges",
		Long: "exchanges lists the exchanges recorded for a session, oldest first: each one's ID, the\n" +
			"harness entries it appended, how it ended, and its prompt. --verify also opens the\n" +
			"session on the harness, which fails if the harness no longer holds the last recorded\n" +
			"entry. Opening starts the harness on the session, which may append entries to it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			recs, checked, err := svc.Exchanges(cmd.Context(), args[0], verify)
			if err != nil {
				return err
			}
			if len(recs) == 0 {
				out.Printf("no exchanges recorded for session %s", args[0])
				return nil
			}
			for _, r := range recs {
				out.Record(r)
			}
			switch {
			case verify && checked:
				out.Printf("verified: the harness still holds the last recorded entry")
			case verify:
				out.Printf("not verified: the harness keeps no journal to check against")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&verify, "verify", false, "check the records against the harness's journal")
	return cmd
}
