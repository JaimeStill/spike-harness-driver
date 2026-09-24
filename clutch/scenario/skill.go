package scenario

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"unicode"

	"github.com/JaimeStill/spike-harness-driver/clutch/domain/session"
	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/catalog"
)

// skills holds the skills the skill scenario embeds, one directory each.
//
//go:embed skills
var skills embed.FS

const (
	// motto is the fact the embedded clutch-motto skill gives, which the model can't know
	// without it.
	motto = "Hold the line, shift the load."

	mottoByName = "/skill:clutch-motto What is the clutch motto? Reply with the motto only."
	mottoPrompt = "What is the clutch motto? Reply with the motto only."

	// weatherSkill is the example skill in clutch/examples/skills, which the skill scenario asks
	// about when --skills loaded it.
	weatherSkill  = "clutch-weather"
	weatherPrompt = "What is the weather at the clutch test site?"
)

// mottoSkill loads the embedded clutch-motto skill through catalog, the loader an external
// skill goes through, so an embedded skill is held to the same rules.
func mottoSkill() (harness.Skill, error) {
	sub, err := fs.Sub(skills, "skills/clutch-motto")
	if err != nil {
		return harness.Skill{}, err
	}
	return catalog.Skill(sub)
}

// skillScenario shows the two ways a skill reaches the model: invoked by name in the prompt,
// with no tools at all, and found by the model itself, which reads the skill with the harness's
// read tool. When --skills loaded the example clutch-weather skill, the model finds that one
// too.
func skillScenario(svc *session.Service, needs []Need) Scenario {
	return Scenario{
		Name:    "skill",
		Summary: "An embedded skill invoked by name and found by the model, and an external one when --skills loads it",
		Needs:   needs,
		Steps: func() ([]Step, func() error) {
			r := &run{svc: svc}
			// ask runs prompt in a new session opened with setup, closing the run's previous
			// session first.
			ask := func(ctx context.Context, rep *Reporter, setup session.Setup, prompt string) (session.Outcome, error) {
				if err := r.close(); err != nil {
					return session.Outcome{}, err
				}
				if err := r.openWith(ctx, rep, "", setup); err != nil {
					return session.Outcome{}, err
				}
				o, err := r.exchange(ctx, rep, session.Exchange{Prompt: prompt})
				return o, ended(o, err)
			}
			return []Step{
				{
					Intent: "Invoke the embedded clutch-motto skill by name, in a session with no tools at all",
					Action: func(ctx context.Context, rep *Reporter) error {
						skill, err := mottoSkill()
						if err != nil {
							return err
						}
						rep.Note("a prompt of /skill:<name> has Pi inline the skill's instructions, so no tool is needed")
						o, err := ask(ctx, rep, session.Setup{Skills: []harness.Skill{skill}, HarnessTools: []string{}}, mottoByName)
						if err != nil {
							return err
						}
						return replyHasPhrase(o.Result.Text, motto, "the motto")
					},
				},
				{
					Intent: "Ask for the motto without naming the skill, in a new session whose only tool is read",
					Action: func(ctx context.Context, rep *Reporter) error {
						skill, err := mottoSkill()
						if err != nil {
							return err
						}
						rep.Note("Pi lists skills to the model only when its read or bash tool is active; the model reads SKILL.md itself")
						o, err := ask(ctx, rep, session.Setup{Skills: []harness.Skill{skill}, HarnessTools: []string{"read"}}, mottoPrompt)
						if err != nil {
							return err
						}
						if err := r.toolRan("read"); err != nil {
							return err
						}
						return replyHasPhrase(o.Result.Text, motto, "the motto")
					},
				},
				{
					Intent: "Ask about the weather only the external clutch-weather skill knows, in a session whose only tool is read",
					Action: func(ctx context.Context, rep *Reporter) error {
						if !slices.ContainsFunc(svc.Skills(), func(s harness.Skill) bool { return s.Name == weatherSkill }) {
							rep.Note("skipped: --skills didn't load a clutch-weather skill; add --skills clutch/examples/skills to include it")
							return nil
						}
						o, err := ask(ctx, rep, session.Setup{HarnessTools: []string{"read"}}, weatherPrompt)
						if err != nil {
							return err
						}
						reply := normalize(o.Result.Text)
						if !strings.Contains(reply, "overcast") || !strings.Contains(strings.ReplaceAll(reply, " ", ""), "northwest") {
							return fmt.Errorf("the reply lacks the clutch-weather report: %q", o.Result.Text)
						}
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

// replyHasPhrase fails unless the reply holds phrase, ignoring case and punctuation, so a model
// that drops the full stop or the quotes still passes. what names phrase in the failure.
func replyHasPhrase(reply, phrase, what string) error {
	if !strings.Contains(normalize(reply), normalize(phrase)) {
		return fmt.Errorf("the reply lacks %s %q: %q", what, phrase, reply)
	}
	return nil
}

// normalize lowercases s and turns every run of characters other than letters and digits into
// one space.
func normalize(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}), " ")
}
