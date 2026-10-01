package conform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync/atomic"
	"time"

	"github.com/JaimeStill/spike-harness-driver/clutch/domain/session"
	"github.com/JaimeStill/spike-harness-driver/clutch/examples/media"
	"github.com/JaimeStill/spike-harness-driver/clutch/scenario"
	"github.com/JaimeStill/spike-harness-driver/harness"
)

// Capability is one thing a harness over a provider is expected to do, checked the same way on
// every cell.
type Capability struct {
	// Name is the matrix column.
	Name string
	run  check
}

// check runs a capability. A nil error is a pass, with a one-line reason; a notApplicable
// error is n/a; any other error is a failure.
type check func(ctx context.Context, e *env) (reason string, err error)

// notApplicable is the error a check returns when the capability doesn't apply to the cell.
type notApplicable string

func (n notApplicable) Error() string { return string(n) }

// execute runs the capability and sorts its error into a status.
func (c Capability) execute(ctx context.Context, e *env) (Status, string) {
	reason, err := c.run(ctx, e)
	var na notApplicable
	switch {
	case err == nil:
		return Pass, reason
	case errors.As(err, &na):
		return NA, na.Error()
	default:
		return Fail, err.Error()
	}
}

// Capabilities lists the capabilities the suite checks, in the matrix's column order.
var Capabilities = []Capability{
	{Name: "exchange", run: checkExchange},
	{Name: "cancel", run: checkCancel},
	{Name: "resume", run: checkResume},
	{Name: "tool", run: checkTool},
	{Name: "skill", run: checkSkill},
	{Name: "structured", run: checkStructured},
	{Name: "vision", run: checkVision},
	{Name: "dropped-image", run: checkDroppedImage},
	{Name: "audio-tool", run: checkAudioTool},
}

// env is what a check runs against: its cell, and where its events go.
type env struct {
	cell       Cell
	capability string
	events     func(Cell, string, harness.Event)
	// latency is the cell's, which the exchange capability fills.
	latency *Latency
	// opened is how long the last open took.
	opened time.Duration
}

// Prompts. Each prompt that wants a structured answer asks for it outright, because a model asked
// to reply in both text and a structured response is told two things. The checks compare the values
// asked for exactly, so no check reads the model's wording.
const (
	exchangePrompt = "Reply with one short sentence: what is Go?"
	cancelPrompt   = "Write a 600-word essay about the history of the Unix operating system."
	// cancelAfter is how many text deltas the cancel capability lets through.
	cancelAfter = 5

	rememberPrompt = "Remember this code word: %s. Reply with the single word OK."
	recallPrompt   = "What is the code word I told you earlier? Provide it as a structured response."
	toolPrompt     = "What is the access code for alice? Use the lookup_code tool, then provide the code as a structured response."
	skillPrompt    = "What is the clutch motto? Provide it as a structured response."
	capitalPrompt  = "What is the capital of France? Provide the city and its country as a structured response."
	colorsPrompt   = "List the three primary colors of pigment (red, yellow, blue) as a structured response, with how many there are."
	shapesPrompt   = "Look at the image. List every shape in it and its color as a structured response."
	// droppedPrompt is the scenario's plain prompt: the capability documents what the harness
	// does with the image, and asks for nothing it could check.
	droppedPrompt = "Name each shape in this image and its color."
	audioPrompt   = "Use the transcribe_recording tool to transcribe the voice recording, then provide the access code it gives as a structured response."

	// shapesType is media.Shapes's media type.
	shapesType = "image/png"
	// phraseCode is the access code the speaker in media.Phrase says.
	phraseCode = "7429"

	lookupTool     = "lookup_code"
	transcribeTool = "transcribe_recording"
)

// The schemas of the structured responses the capabilities ask for.
var (
	wordSchema = json.RawMessage(`{"type":"object","properties":{` +
		`"word":{"type":"string","description":"The code word."}},"required":["word"]}`)
	codeSchema = json.RawMessage(`{"type":"object","properties":{` +
		`"code":{"type":"string","description":"The access code."}},"required":["code"]}`)
	mottoSchema = json.RawMessage(`{"type":"object","properties":{` +
		`"motto":{"type":"string","description":"The motto."}},"required":["motto"]}`)
	capitalSchema = json.RawMessage(`{"type":"object","properties":{` +
		`"city":{"type":"string","description":"The capital city."},` +
		`"country":{"type":"string","description":"The country whose capital it is."}},"required":["city","country"]}`)
	colorsSchema = json.RawMessage(`{"type":"object","properties":{` +
		`"count":{"type":"integer","description":"How many colors are listed."},` +
		`"colors":{"type":"array","items":{"type":"string"},"description":"The colors."}},"required":["count","colors"]}`)
	shapesSchema = json.RawMessage(`{"type":"object","properties":{` +
		`"shapes":{"type":"array","description":"Every shape in the image.","items":{"type":"object","properties":{` +
		`"shape":{"type":"string","description":"The shape's name."},` +
		`"color":{"type":"string","description":"The shape's color."}},"required":["shape","color"]}}},"required":["shapes"]}`)
	noArgsSchema = json.RawMessage(`{"type":"object","properties":{}}`)
	nameSchema   = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"The person's name."}},"required":["name"]}`)
)

// open opens the session id names with setup, or a new one for an empty id, and runs fn on it,
// closing it after. A failure to close is an error when fn found none.
func (e *env) open(ctx context.Context, id string, setup session.Setup, fn func(*harness.Session) error) (err error) {
	start := time.Now()
	sess, err := e.cell.Service.OpenWith(ctx, id, setup)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	e.opened = time.Since(start)
	defer func() {
		if cerr := sess.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close: %w", cerr)
		}
	}()
	return fn(sess)
}

// send runs ex on sess to its end and returns how it ended and its events. The error is
// non-nil only when the harness didn't accept the prompt.
func (e *env) send(ctx context.Context, sess *harness.Session, ex session.Exchange) (session.Outcome, []harness.Event, error) {
	var events []harness.Event
	o, err := e.cell.Service.Run(ctx, sess, ex, session.Observer{Event: func(ev harness.Event) {
		events = append(events, ev)
		if e.events != nil {
			e.events(e.cell, e.capability, ev)
		}
	}})
	return o, events, err
}

// ask is send for an exchange that must end on its own: it fails on an error, a cancellation,
// and a stop reason of error.
func (e *env) ask(ctx context.Context, sess *harness.Session, ex session.Exchange) (session.Outcome, []harness.Event, error) {
	o, events, err := e.send(ctx, sess, ex)
	if err != nil {
		return o, events, err
	}
	return o, events, ended(o)
}

// ended fails unless o ended normally.
func ended(o session.Outcome) error {
	switch {
	case o.Err != nil:
		return fmt.Errorf("the exchange ended in error: %w", o.Err)
	case o.Result.StopReason == "aborted" || o.Result.StopReason == "error":
		return fmt.Errorf("the exchange stopped with %q", o.Result.StopReason)
	}
	return nil
}

// askFor asks prompt with schema, and decodes the structured response into v.
func (e *env) askFor(ctx context.Context, sess *harness.Session, prompt string, schema json.RawMessage, v any) ([]harness.Event, error) {
	o, events, err := e.ask(ctx, sess, session.Exchange{Prompt: prompt, Schema: schema})
	if err != nil {
		return events, err
	}
	return events, decode(o.Result.Structured, v)
}

// noTools opens a session with none of the harness's own tools, which leaves a structured
// response, and the Go tool a capability offers, the only things the model can use.
var noTools = session.Setup{HarnessTools: []string{}}

// checkExchange runs a plain exchange, and measures the cell's latency on it: the session's
// open, and the time from sending to the first text and to the end.
func checkExchange(ctx context.Context, e *env) (string, error) {
	var reason string
	err := e.open(ctx, "", session.Setup{}, func(sess *harness.Session) error {
		start := time.Now()
		var first time.Duration
		o, err := e.cell.Service.Run(ctx, sess, session.Exchange{Prompt: exchangePrompt}, session.Observer{Event: func(ev harness.Event) {
			if first == 0 && ev.Kind == harness.EventTextDelta {
				first = time.Since(start)
			}
			if e.events != nil {
				e.events(e.cell, e.capability, ev)
			}
		}})
		turn := time.Since(start)
		if err == nil {
			err = ended(o)
		}
		if err != nil {
			return err
		}
		if strings.TrimSpace(o.Result.Text) == "" {
			return errors.New("the exchange ended with no text")
		}
		if e.latency != nil {
			*e.latency = Latency{Open: e.opened, FirstText: first, Turn: turn}
		}
		reason = fmt.Sprintf("ended with stop reason %q and %d characters of text", o.Result.StopReason, len(o.Result.Text))
		return nil
	})
	return reason, err
}

func checkCancel(ctx context.Context, e *env) (string, error) {
	var reason string
	err := e.open(ctx, "", session.Setup{}, func(sess *harness.Session) error {
		o, _, err := e.send(ctx, sess, session.Exchange{Prompt: cancelPrompt, CancelAfter: cancelAfter})
		if err != nil {
			return err
		}
		if o.Result.StopReason != "aborted" {
			return fmt.Errorf("cancelled after %d text deltas, the exchange stopped with %q, want %q", cancelAfter, o.Result.StopReason, "aborted")
		}
		follow, _, err := e.ask(ctx, sess, session.Exchange{Prompt: exchangePrompt})
		if err != nil {
			return fmt.Errorf("the exchange after the cancellation: %w", err)
		}
		if strings.TrimSpace(follow.Result.Text) == "" {
			return errors.New("the exchange after the cancellation ended with no text")
		}
		reason = fmt.Sprintf("aborted after %d text deltas; the next exchange on the session stopped with %q", cancelAfter, follow.Result.StopReason)
		return nil
	})
	return reason, err
}

// codeWords are the common words a code word is made of, so a model copies them as written.
var codeWords = []string{"amber", "falcon", "harbor", "meadow", "copper", "lantern", "willow", "summit", "ember", "cobalt"}

// newCodeWord returns a code word no earlier run used, for the resume capability: two words and
// a number, which a model can't guess and can only know from the first exchange.
func newCodeWord() string {
	pick := func() string { return codeWords[rand.IntN(len(codeWords))] }
	return fmt.Sprintf("%s-%s-%03d", pick(), pick(), rand.IntN(1000))
}

func checkResume(ctx context.Context, e *env) (string, error) {
	word := newCodeWord()
	var id string
	err := e.open(ctx, "", noTools, func(sess *harness.Session) error {
		id = sess.ID()
		_, _, err := e.ask(ctx, sess, session.Exchange{Prompt: fmt.Sprintf(rememberPrompt, word)})
		return err
	})
	if err != nil {
		return "", fmt.Errorf("the first exchange: %w", err)
	}
	// The session is closed, so the harness process has ended; the service builds a new driver
	// for each open, and the second session is a new process resuming the first's history.
	var got struct {
		Word string `json:"word"`
	}
	err = e.open(ctx, id, noTools, func(sess *harness.Session) error {
		if sess.ID() != id {
			return fmt.Errorf("resumed session %s, want %s", sess.ID(), id)
		}
		_, err := e.askFor(ctx, sess, recallPrompt, wordSchema, &got)
		return err
	})
	if err != nil {
		return "", fmt.Errorf("after resuming session %s: %w", id, err)
	}
	if err := sameValue("the code word", got.Word, word); err != nil {
		return "", err
	}
	return fmt.Sprintf("session %s resumed in a new process, and recalled %q", shortID(id), word), nil
}

// newCode returns a code no earlier run used, for the tool capability.
func newCode() string { return fmt.Sprintf("ZX-%06X", rand.IntN(1<<24)) }

// calls counts the calls a Go tool's handler received, which is the proof the model called it
// and the driver ran it here, whatever the model then said.
type calls struct{ n atomic.Int32 }

// goTool returns a Go tool whose handler counts its calls in c and answers with what result
// returns for the call's arguments.
func (c *calls) goTool(name, description string, schema json.RawMessage, result func(ctx context.Context, args json.RawMessage) (string, error)) harness.Tool {
	return harness.Tool{
		Name:        name,
		Description: description,
		Schema:      schema,
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			c.n.Add(1)
			return result(ctx, args)
		},
	}
}

func checkTool(ctx context.Context, e *env) (string, error) {
	want := newCode()
	var c calls
	tool := c.goTool(lookupTool, "Looks up the access code for a person by name.", nameSchema,
		func(_ context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.Name) == "" {
				return "", fmt.Errorf("%s: want a name", lookupTool)
			}
			return want, nil
		})
	var reason string
	err := e.open(ctx, "", session.Setup{Tools: []harness.Tool{tool}, HarnessTools: []string{}}, func(sess *harness.Session) error {
		var got struct {
			Code string `json:"code"`
		}
		events, err := e.askFor(ctx, sess, toolPrompt, codeSchema, &got)
		if err != nil {
			return err
		}
		if err := toolRan(events, lookupTool); err != nil {
			return err
		}
		if c.n.Load() == 0 {
			return fmt.Errorf("the %s handler never ran", lookupTool)
		}
		if err := sameValue("the access code", got.Code, want); err != nil {
			return err
		}
		reason = fmt.Sprintf("the %s handler ran %d time(s), and the structured code was %s", lookupTool, c.n.Load(), want)
		return nil
	})
	return reason, err
}

func checkSkill(ctx context.Context, e *env) (string, error) {
	skill, motto, err := scenario.MottoSkill()
	if err != nil {
		return "", err
	}
	p := e.cell.Profile
	var reason string
	err = e.open(ctx, "", session.Setup{Skills: []harness.Skill{skill}, HarnessTools: []string{p.SkillTool}}, func(sess *harness.Session) error {
		var got struct {
			Motto string `json:"motto"`
		}
		if _, err := e.askFor(ctx, sess, skillPrompt, mottoSchema, &got); err != nil {
			return err
		}
		if err := sameMotto(got.Motto, motto); err != nil {
			return err
		}
		reason = fmt.Sprintf("the structured motto matched the skill's, with only the %s tool", p.SkillTool)
		return nil
	})
	return reason, err
}

func checkStructured(ctx context.Context, e *env) (string, error) {
	var reason string
	err := e.open(ctx, "", noTools, func(sess *harness.Session) error {
		var city struct {
			City    string `json:"city"`
			Country string `json:"country"`
		}
		if _, err := e.askFor(ctx, sess, capitalPrompt, capitalSchema, &city); err != nil {
			return fmt.Errorf("the capital: %w", err)
		}
		if err := checkCapital(city.City, city.Country); err != nil {
			return err
		}
		var colors struct {
			Count  int      `json:"count"`
			Colors []string `json:"colors"`
		}
		if _, err := e.askFor(ctx, sess, colorsPrompt, colorsSchema, &colors); err != nil {
			return fmt.Errorf("the colors: %w", err)
		}
		if err := checkColors(colors.Count, colors.Colors); err != nil {
			return err
		}
		reason = "two schemas on one session, both decoded with the expected values"
		return nil
	})
	return reason, err
}

// shapeColor is one entry of the vision capability's structured response.
type shapeColor struct {
	Shape string `json:"shape"`
	Color string `json:"color"`
}

func checkVision(ctx context.Context, e *env) (string, error) {
	var reason string
	setup := session.Setup{Model: e.cell.VisionModel, HarnessTools: []string{}}
	err := e.open(ctx, "", setup, func(sess *harness.Session) error {
		var got struct {
			Shapes []shapeColor `json:"shapes"`
		}
		o, _, err := e.ask(ctx, sess, session.Exchange{
			Prompt: shapesPrompt,
			Images: []harness.Image{{MediaType: shapesType, Data: media.Shapes}},
			Schema: shapesSchema,
		})
		if err != nil {
			return err
		}
		if err := decode(o.Result.Structured, &got); err != nil {
			return err
		}
		if err := checkShapes(got.Shapes); err != nil {
			return err
		}
		reason = fmt.Sprintf("%d shapes, each with its color, on %s", len(got.Shapes), e.cell.VisionModel)
		return nil
	})
	return reason, err
}

// replyCut is how much of a reply a reason keeps.
const replyCut = 60

func checkDroppedImage(ctx context.Context, e *env) (string, error) {
	p := e.cell.Profile
	switch {
	case !p.DropsImage:
		return "", notApplicable(p.Name + " drops no image: every model it runs takes images")
	case e.cell.DefaultTakesImages:
		return "", notApplicable("the provider's default model takes images, so there is no text-only model to drop one")
	}
	var reason string
	err := e.open(ctx, "", noTools, func(sess *harness.Session) error {
		o, _, err := e.ask(ctx, sess, session.Exchange{
			Prompt: droppedPrompt,
			Images: []harness.Image{{MediaType: shapesType, Data: media.Shapes}},
		})
		if err != nil {
			return err
		}
		// The harness's text for a dropped image is behavior to record, not wording to judge.
		reason = fmt.Sprintf("the exchange ended without error; %s replied %q", p.Name, truncate(o.Result.Text, replyCut))
		return nil
	})
	return reason, err
}

func checkAudioTool(ctx context.Context, e *env) (string, error) {
	m, err := e.cell.Models()
	if err != nil {
		return "", notApplicable("no direct model client: " + err.Error())
	}
	var c calls
	tool := c.goTool(transcribeTool, "Transcribes the voice recording the user refers to, and returns its text.", noArgsSchema,
		func(hctx context.Context, _ json.RawMessage) (string, error) {
			text, err := scenario.TranscribeRecording(hctx, m)
			if err != nil {
				return "", fmt.Errorf("%s: %w", transcribeTool, err)
			}
			return text, nil
		})
	var reason string
	err = e.open(ctx, "", session.Setup{Tools: []harness.Tool{tool}, HarnessTools: []string{}}, func(sess *harness.Session) error {
		var got struct {
			Code string `json:"code"`
		}
		events, err := e.askFor(ctx, sess, audioPrompt, codeSchema, &got)
		if err != nil {
			return err
		}
		if err := toolRan(events, transcribeTool); err != nil {
			return err
		}
		if c.n.Load() == 0 {
			return fmt.Errorf("the %s handler never ran", transcribeTool)
		}
		if err := sameValue("the access code", got.Code, phraseCode); err != nil {
			return err
		}
		reason = fmt.Sprintf("transcribed through %s, and the structured code was %s", m.Target, phraseCode)
		return nil
	})
	return reason, err
}
