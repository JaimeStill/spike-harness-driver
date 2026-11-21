package scenario

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/JaimeStill/spike-harness-driver/clutch/domain/session"
	"github.com/JaimeStill/spike-harness-driver/clutch/examples/media"
	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/model"
)

const (
	phraseCode = "7429"
	phraseFile = "phrase.wav"

	audioPrompt      = "What access code does the speaker say?"
	transcribePrompt = "Use the transcribe_recording tool to transcribe the voice recording, then tell me the access code it gives."

	transcribeTool = "transcribe_recording"
)

var transcribeSchema = json.RawMessage(`{"type":"object","properties":{}}`)

var digitWords = map[string]string{
	"zero": "0", "one": "1", "two": "2", "three": "3", "four": "4",
	"five": "5", "six": "6", "seven": "7", "eight": "8", "nine": "9",
}

func audioScenario(svc *session.Service, models func() (Models, error), needs []Need) Scenario {
	return Scenario{
		Name:    "audio",
		Summary: "A recording transcribed by the direct client, heard in chat, and transcribed by a Go tool the model calls",
		Needs:   needs,
		Steps: func() ([]Step, func() error) {
			r := &run{svc: svc}
			return []Step{
				{
					Intent: "Transcribe the recording with the direct client",
					Action: func(ctx context.Context, rep *Reporter) error {
						m, err := models()
						if err != nil {
							return err
						}
						rep.Note("model %s on target %s", m.AudioModel, m.Target)
						text, err := TranscribeRecording(ctx, m)
						if err != nil {
							return err
						}
						rep.Note("transcript: %q", text)
						return hasCode(text)
					},
				},
				{
					Intent: "Ask a chat model what the speaker says, with the recording as a part of the message",
					Action: func(ctx context.Context, rep *Reporter) error {
						m, err := models()
						if err != nil {
							return err
						}
						if !m.AudioInChat {
							rep.Note("skipped: target %s's chat models take no audio input; its audio model only transcribes", m.Target)
							return nil
						}
						rep.Note("model %s on target %s", m.AudioModel, m.Target)
						resp, err := m.Chat.Chat(ctx, model.ChatRequest{
							Model: m.AudioModel,
							Messages: []model.Message{{Role: model.RoleUser, Parts: []model.Part{
								model.Text{Text: audioPrompt},
								model.Audio{Format: "wav", Data: media.Phrase},
							}}},
						})
						if err != nil {
							return err
						}
						noteChat(rep, resp)
						return hasCode(resp.Text)
					},
				},
				{
					Intent: "Open a harness session on the default model offering the Go tool transcribe_recording, and ask for the code",
					Action: func(ctx context.Context, rep *Reporter) error {
						m, err := models()
						if err != nil {
							return err
						}
						rep.Note("transcribe_recording's handler calls the direct client, so the harness's model hears the recording through a tool")
						if err := r.openWith(ctx, rep, "", session.Setup{
							Tools:        []harness.Tool{transcribeRecordingTool(rep, m)},
							HarnessTools: []string{},
						}); err != nil {
							return err
						}
						o, err := r.exchange(ctx, rep, session.Exchange{Prompt: transcribePrompt})
						if err := ended(o, err); err != nil {
							return err
						}
						if err := r.toolRan(transcribeTool); err != nil {
							return err
						}
						return hasCode(o.Result.Text)
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

// TranscribeRecording returns media.Phrase's transcript from m's audio client. It is the Go
// tool's work, exported for a caller that offers a tool of its own.
func TranscribeRecording(ctx context.Context, m Models) (string, error) {
	t, err := m.Audio.Transcribe(ctx, model.TranscribeRequest{
		Model:    m.AudioModel,
		Audio:    bytes.NewReader(media.Phrase),
		Filename: phraseFile,
	})
	return t.Text, err
}

func transcribeRecordingTool(rep *Reporter, m Models) harness.Tool {
	return harness.Tool{
		Name:        transcribeTool,
		Description: "Transcribes the voice recording the user refers to, and returns its text.",
		Schema:      transcribeSchema,
		Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
			text, err := TranscribeRecording(ctx, m)
			if err != nil {
				return "", fmt.Errorf("%s: %w", transcribeTool, err)
			}
			rep.Note("go handler: %s() → %q", transcribeTool, text)
			return text, nil
		},
	}
}

func hasCode(text string) error {
	for _, run := range digitRuns(text) {
		if run == phraseCode {
			return nil
		}
	}
	return fmt.Errorf("the reply lacks the access code %s: %q", phraseCode, text)
}

const runBreaks = ".!?;:()[]{}\n"

func digitRuns(text string) []string {
	var runs []string
	var run, word strings.Builder
	end := func() {
		if run.Len() > 0 {
			runs = append(runs, run.String())
			run.Reset()
		}
	}
	endWord := func() {
		w := word.String()
		word.Reset()
		if w == "" {
			return
		}
		if d, ok := digitWords[w]; ok {
			w = d
		}
		if strings.Trim(w, "0123456789") != "" {
			end()
			return
		}
		run.WriteString(w)
	}
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word.WriteRune(r)
		case strings.ContainsRune(runBreaks, r):
			endWord()
			end()
		default:
			endWord()
		}
	}
	endWord()
	end()
	return runs
}
