package scenario

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/JaimeStill/spike-harness-driver/clutch/domain/session"
	"github.com/JaimeStill/spike-harness-driver/harness"
)

const (
	capitalPrompt = "What is the capital of France?"
	colorsPrompt  = "List the three primary colors of pigment (red, yellow, blue)."
	// plainPrompt asks the model not to answer through respond, to show the contract when the
	// model may not comply.
	plainPrompt = "Reply in plain text only, without calling any tool: say hello."

	// respondTool is the tool the Pi adapter's bridge offers for an exchange with a schema;
	// its validated arguments are the structured response. The harness surface has no
	// harness-neutral signal for a failed validation, so the narration counts respond's failed
	// calls by name, which holds while Pi is the only adapter.
	respondTool = "respond"
)

// capitalSchema, colorsSchema, and greetingSchema are the structured scenario's response
// schemas, a different shape for each exchange on the one session.
var (
	capitalSchema = json.RawMessage(`{"type":"object","properties":{` +
		`"city":{"type":"string","description":"The capital city."},` +
		`"country":{"type":"string","description":"The country whose capital it is."}},` +
		`"required":["city","country"]}`)
	colorsSchema = json.RawMessage(`{"type":"object","properties":{` +
		`"count":{"type":"integer","description":"How many colors are listed."},` +
		`"colors":{"type":"array","items":{"type":"string"},"minItems":3,"maxItems":3,"description":"The colors."}},` +
		`"required":["count","colors"]}`)
	greetingSchema = json.RawMessage(`{"type":"object","properties":{` +
		`"greeting":{"type":"string","description":"The greeting."}},` +
		`"required":["greeting"]}`)
)

// capital, colors, and greeting are the Go types the structured scenario decodes each
// response into.
type (
	capital struct {
		City    string `json:"city"`
		Country string `json:"country"`
	}
	colors struct {
		Count  int      `json:"count"`
		Colors []string `json:"colors"`
	}
	greeting struct {
		Greeting string `json:"greeting"`
	}
)

// structuredScenario asks for structured responses on one session with no harness tools: each
// exchange carries its own schema, so the respond tool the harness offers takes a new shape
// each time. The last exchange asks the model not to answer through respond, and checks the
// contract whichever way the model goes.
func structuredScenario(svc *session.Service, needs []Need) Scenario {
	return Scenario{
		Name:    "structured",
		Summary: "Structured responses of a different shape per exchange, validated against each exchange's schema",
		Needs:   needs,
		Steps: func() ([]Step, func() error) {
			r := &run{svc: svc}
			// ask runs prompt on the run's session with schema, notes any validation retries,
			// and fails unless the exchange ended with a response that decodes into v.
			ask := func(ctx context.Context, rep *Reporter, prompt string, schema json.RawMessage, v any) error {
				o, err := r.exchange(ctx, rep, session.Exchange{Prompt: prompt, Schema: schema})
				if err != nil {
					return err
				}
				r.noteRetries(rep)
				if err := ended(o, nil); err != nil {
					return err
				}
				return decode(o.Result.Structured, v)
			}
			return []Step{
				{
					Intent: "Open a session with none of the harness's own tools; respond is the only one, offered per exchange",
					Action: func(ctx context.Context, rep *Reporter) error {
						return r.openWith(ctx, rep, "", session.Setup{HarnessTools: []string{}})
					},
				},
				{
					Intent: "Ask for a capital as {city, country}, and decode the response into a Go struct",
					Action: func(ctx context.Context, rep *Reporter) error {
						rep.Note("the harness validates respond's arguments against the schema, so a failed call goes back to the model")
						var v capital
						if err := ask(ctx, rep, capitalPrompt, capitalSchema, &v); err != nil {
							return err
						}
						rep.Note("decoded: %+v", v)
						if !strings.Contains(strings.ToLower(v.City), "paris") {
							return fmt.Errorf("the city is %q, want Paris", v.City)
						}
						return nil
					},
				},
				{
					Intent: "On the same session, ask for the primary colors as {count, colors}, a different shape",
					Action: func(ctx context.Context, rep *Reporter) error {
						rep.Note("the schema is per exchange: respond takes this exchange's shape on the same session")
						var v colors
						if err := ask(ctx, rep, colorsPrompt, colorsSchema, &v); err != nil {
							return err
						}
						rep.Note("decoded: %+v", v)
						if v.Count != 3 || len(v.Colors) != 3 {
							return fmt.Errorf("count %d with %d colors, want 3 and 3", v.Count, len(v.Colors))
						}
						return nil
					},
				},
				{
					Intent: "Ask for plain text with a schema set, and check the contract whichever way the model goes",
					Action: func(ctx context.Context, rep *Reporter) error {
						// ended fails on any outcome error, and ErrNoStructuredResponse is one this
						// step accepts, so the step reads the outcome itself.
						o, err := r.exchange(ctx, rep, session.Exchange{Prompt: plainPrompt, Schema: greetingSchema})
						if err != nil {
							return err
						}
						r.noteRetries(rep)
						switch {
						case errors.Is(o.Err, harness.ErrNoStructuredResponse):
							rep.Note("no structured response: the exchange ended with ErrNoStructuredResponse")
							return nil
						case o.Err != nil:
							return fmt.Errorf("exchange ended in error: %w", o.Err)
						}
						var v greeting
						if err := decode(o.Result.Structured, &v); err != nil {
							return err
						}
						rep.Note("the model answered through respond anyway; decoded: %+v", v)
						return nil
					},
				},
				{
					Intent: "Close the session, which ends the harness process",
					Action: func(context.Context, *Reporter) error { return r.close() },
				},
			}, r.close
		},
	}
}

// decode decodes a structured response into v, failing when there is none.
func decode(structured json.RawMessage, v any) error {
	if len(structured) == 0 {
		return errors.New("the exchange ended without a structured response")
	}
	if err := json.Unmarshal(structured, v); err != nil {
		return fmt.Errorf("decode the structured response %s: %w", structured, err)
	}
	return nil
}

// noteRetries notes how many times the run's last exchange called respond with arguments that
// failed validation, each of which the harness returned to the model as a failed tool call.
func (r *run) noteRetries(rep *Reporter) {
	n := 0
	for _, ev := range r.events {
		if ev.Kind == harness.EventToolResult && ev.Tool != nil && ev.Tool.Name == respondTool && ev.Tool.IsError {
			n++
		}
	}
	if n > 0 {
		rep.Note("respond failed validation %d time(s); the model retried", n)
	}
}
