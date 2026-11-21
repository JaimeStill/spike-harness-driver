package scenario

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/JaimeStill/spike-harness-driver/clutch/domain/session"
	"github.com/JaimeStill/spike-harness-driver/harness"
)

const (
	lookupPerson = "alice"
	lookupPrompt = "What is the access code for alice? Use the lookup_code tool, then reply with the code only."

	fingerprintTool   = "fingerprint"
	fingerprintText   = "clutch"
	fingerprintPrompt = "Fingerprint the text clutch with the fingerprint tool, then reply with the fingerprint only."
)

var lookupSchema = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"The person's name."}},"required":["name"]}`)

func toolScenario(svc *session.Service, needs []Need) Scenario {
	return Scenario{
		Name:    "tool",
		Summary: "The model calls a Go tool, and an external command tool when --tools loads one",
		Needs:   needs,
		Steps: func() ([]Step, func() error) {
			r := &run{svc: svc}
			return []Step{
				{
					Intent: "Open a session offering the Go tool lookup_code and none of the harness's own tools",
					Action: func(ctx context.Context, rep *Reporter) error {
						rep.Note("lookup_code's handler runs in this process; the harness calls it back through the driver")
						return r.openWith(ctx, rep, "", session.Setup{
							Tools:        []harness.Tool{lookupCodeTool(rep)},
							HarnessTools: []string{},
						})
					},
				},
				{
					Intent: "Ask for an access code only the lookup_code tool knows",
					Action: func(ctx context.Context, rep *Reporter) error {
						o, err := r.exchange(ctx, rep, session.Exchange{Prompt: lookupPrompt})
						if err := ended(o, err); err != nil {
							return err
						}
						if err := r.toolRan("lookup_code"); err != nil {
							return err
						}
						return replyHas(o.Result.Text, accessCode(lookupPerson), "the access code")
					},
				},
				{
					Intent: "Ask for a fingerprint only the external fingerprint command tool computes",
					Action: func(ctx context.Context, rep *Reporter) error {
						if !slices.ContainsFunc(svc.Tools(), func(t harness.Tool) bool { return t.Name == fingerprintTool }) {
							rep.Note("skipped: --tools didn't load a fingerprint tool; add --tools clutch/examples/tools to include it")
							return nil
						}
						rep.Note("fingerprint is a command tool: each call runs clutch/examples/tools/fingerprint/run.py")
						o, err := r.exchange(ctx, rep, session.Exchange{Prompt: fingerprintPrompt})
						if err := ended(o, err); err != nil {
							return err
						}
						if err := r.toolRan(fingerprintTool); err != nil {
							return err
						}
						return replyHas(o.Result.Text, fingerprint(fingerprintText), "the fingerprint")
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

func lookupCodeTool(rep *Reporter) harness.Tool {
	return harness.Tool{
		Name:        "lookup_code",
		Description: "Looks up the access code for a person by name.",
		Schema:      lookupSchema,
		Handler: func(_ context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", fmt.Errorf("lookup_code: %w", err)
			}
			if strings.TrimSpace(in.Name) == "" {
				return "", errors.New("lookup_code: name is empty")
			}
			code := accessCode(in.Name)
			rep.Note("go handler: lookup_code(%q) → %s", in.Name, code)
			return code, nil
		},
	}
}

func accessCode(name string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(name))))
	return "ZX-" + strings.ToUpper(hex.EncodeToString(sum[:3]))
}

func fingerprint(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:6])
}

func replyHas(reply, want, what string) error {
	if !strings.Contains(strings.ToLower(reply), strings.ToLower(want)) {
		return fmt.Errorf("the reply lacks %s %s: %q", what, want, reply)
	}
	return nil
}
