package scenario

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/JaimeStill/spike-harness-driver/clutch/domain/session"
	"github.com/JaimeStill/spike-harness-driver/clutch/examples/media"
	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/model"
)

const (
	shapesPrompt = "Name each shape in this image and its color."
	// shapesType is media.Shapes's media type.
	shapesType = "image/png"
)

// shapes pairs each shape in media.Shapes with its color.
var shapes = [][2]string{{"circle", "red"}, {"square", "blue"}, {"triangle", "green"}}

// colorWords are the colors namesShapes looks for, the fixture's three and others a model might
// name, so a wrong color is seen as one rather than skipped.
var colorWords = []string{
	"red", "blue", "green", "yellow", "orange", "purple", "violet", "pink", "brown", "black",
	"white", "gray", "grey", "cyan", "magenta",
}

// visionScenario sends one image three ways: to a harness session on a model that takes
// images, to the direct client, and to a harness session on the text-only default model,
// which shows what Pi does with an image such a model can't take.
func visionScenario(svc *session.Service, models func() (Models, error), needs []Need) Scenario {
	return Scenario{
		Name:    "vision",
		Summary: "An image read through the harness and the direct client, and dropped for a text-only model",
		Needs:   needs,
		Steps: func() ([]Step, func() error) {
			r := &run{svc: svc}
			image := []harness.Image{{MediaType: shapesType, Data: media.Shapes}}
			// ask sends the shapes image in a new session with no harness tools, on modelID, or
			// the default model when it is empty, closing the run's previous session first.
			ask := func(ctx context.Context, rep *Reporter, modelID string) (session.Outcome, error) {
				if err := r.close(); err != nil {
					return session.Outcome{}, err
				}
				if err := r.openWith(ctx, rep, "", session.Setup{Model: modelID, HarnessTools: []string{}}); err != nil {
					return session.Outcome{}, err
				}
				o, err := r.exchange(ctx, rep, session.Exchange{Prompt: shapesPrompt, Images: image})
				return o, ended(o, err)
			}
			return []Step{
				{
					Intent: "Send the shapes image to a harness session on a model that takes images",
					Action: func(ctx context.Context, rep *Reporter) error {
						m, err := models()
						if err != nil {
							return err
						}
						rep.Note("the harness session runs on %s; the image goes with the prompt", m.HarnessVision)
						o, err := ask(ctx, rep, m.HarnessVision)
						if err != nil {
							return err
						}
						return namesShapes(o.Result.Text)
					},
				},
				{
					Intent: "Ask the direct client the same, with the image as a part of the chat message",
					Action: func(ctx context.Context, rep *Reporter) error {
						m, err := models()
						if err != nil {
							return err
						}
						rep.Note("model %s on target %s", m.VisionModel, m.Target)
						resp, err := m.Chat.Chat(ctx, model.ChatRequest{
							Model: m.VisionModel,
							Messages: []model.Message{{Role: model.RoleUser, Parts: []model.Part{
								model.Text{Text: shapesPrompt},
								model.Image{MediaType: shapesType, Data: media.Shapes},
							}}},
						})
						if err != nil {
							return err
						}
						noteChat(rep, resp)
						return namesShapes(resp.Text)
					},
				},
				{
					Intent: "Send the same image to a harness session on the text-only default model",
					Action: func(ctx context.Context, rep *Reporter) error {
						rep.Note("Pi replaces an image with \"(image omitted: model does not support images)\" for a model")
						rep.Note("whose entry lists no image input, and runs the exchange anyway")
						o, err := ask(ctx, rep, "")
						if err != nil {
							return err
						}
						if namesShapes(o.Result.Text) == nil {
							return fmt.Errorf("the text-only model named every shape and its color: %q", o.Result.Text)
						}
						rep.Note("the exchange succeeded without the image: Pi dropped it quietly, and the reply can't name the shapes")
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

// noteChat notes a direct chat response: its text, why it stopped, and its token usage.
func noteChat(rep *Reporter, resp model.ChatResponse) {
	rep.Note("reply: %q", resp.Text)
	rep.Note("finish=%s usage=%d/%d", resp.FinishReason, resp.Usage.InputTokens, resp.Usage.OutputTokens)
}

// namesShapes fails unless the reply names each shape in media.Shapes with its color. A shape
// counts as named with its color when, at one of its mentions, the color word nearest it is
// that color, so "a red circle", "the circle is red", and a table row "| Circle | Red |" all
// pass, while a reply that swaps two colors doesn't. It ignores case.
func namesShapes(reply string) error {
	toks := tokenize(reply)
	var missing []string
	for _, sc := range shapes {
		if !shapeHasColor(toks, sc[0], sc[1]) {
			missing = append(missing, sc[1]+" "+sc[0])
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("the reply lacks the %s: %q", strings.Join(missing, ", "), reply)
	}
	return nil
}

// token is a lowercase word of letters and digits, or, when brk is set, a mark that ends a
// clause: a comma, a semicolon, a sentence's end, or a line's.
type token struct {
	word string
	brk  bool
}

// tokenize splits s into words and clause breaks, dropping every other character.
func tokenize(s string) []token {
	var toks []token
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			toks = append(toks, token{word: word.String()})
			word.Reset()
		}
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word.WriteRune(r)
		case strings.ContainsRune(",;.!?\n", r):
			flush()
			toks = append(toks, token{brk: true})
		default:
			flush()
		}
	}
	flush()
	return toks
}

// shapeHasColor reports whether, at some mention of shape, the nearest color word is color.
// Nearest counts the clause breaks crossed first and the words between second, so a color in
// the shape's own clause wins over a closer one across a comma or a line's end. A full tie goes
// to the color before, as an adjective precedes its noun in English.
func shapeHasColor(toks []token, shape, color string) bool {
	for i, t := range toks {
		if t.word != shape && t.word != shape+"s" {
			continue
		}
		before, bOK := nearestColor(toks, i, -1)
		after, aOK := nearestColor(toks, i, +1)
		switch {
		case bOK && (!aOK || !after.closer(before)):
			if before.word == color {
				return true
			}
		case aOK:
			if after.word == color {
				return true
			}
		}
	}
	return false
}

// colorHit is the first color word found walking from a shape in one direction.
type colorHit struct {
	word          string
	breaks, words int
}

// closer reports whether h is nearer its shape than o.
func (h colorHit) closer(o colorHit) bool {
	if h.breaks != o.breaks {
		return h.breaks < o.breaks
	}
	return h.words < o.words
}

// nearestColor walks from toks[i] in direction step to the first color word.
func nearestColor(toks []token, i, step int) (colorHit, bool) {
	var h colorHit
	for j := i + step; j >= 0 && j < len(toks); j += step {
		t := toks[j]
		if t.brk {
			h.breaks++
			continue
		}
		h.words++
		if slices.Contains(colorWords, t.word) {
			h.word = t.word
			return h, true
		}
	}
	return h, false
}
