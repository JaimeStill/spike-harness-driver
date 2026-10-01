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
	// phraseCode is the access code the speaker in media.Phrase says.
	phraseCode = "7429"
	// phraseFile is media.Phrase's filename, whose extension tells the endpoint its format.
	phraseFile = "phrase.wav"

	audioPrompt      = "What access code does the speaker say?"
	transcribePrompt = "Use the transcribe_recording tool to transcribe the voice recording, then tell me the access code it gives."

	// transcribeTool is the Go tool that hands the harness's model a transcript.
	transcribeTool = "transcribe_recording"
)

// transcribeSchema is transcribe_recording's argument schema. The tool takes no arguments,
// because it holds the recording.
var transcribeSchema = json.RawMessage(`{"type":"object","properties":{}}`)

// digitWords maps each spoken digit to its numeral.
var digitWords = map[string]string{
	"zero": "0", "one": "1", "two": "2", "three": "3", "four": "4",
	"five": "5", "six": "6", "seven": "7", "eight": "8", "nine": "9",
}

// audioScenario transcribes a recording with the direct client, asks a chat model about it
// when the target's audio model takes audio in chat, and then gives the recording to a harness
// session through a Go tool that transcribes it. The harness takes no audio itself, so a tool
// is how an agent gets a transcript.
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
						text, err := transcribe(ctx, m)
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

// TranscribeRecording is the Go tool's work, for a caller that offers its own: it returns
// media.Phrase's transcript from m's audio client.
func TranscribeRecording(ctx context.Context, m Models) (string, error) { return transcribe(ctx, m) }

// transcribe returns media.Phrase's transcript from m's audio client.
func transcribe(ctx context.Context, m Models) (string, error) {
	t, err := m.Audio.Transcribe(ctx, model.TranscribeRequest{
		Model:    m.AudioModel,
		Audio:    bytes.NewReader(media.Phrase),
		Filename: phraseFile,
	})
	return t.Text, err
}

// transcribeRecordingTool is a Go tool whose handler transcribes media.Phrase with m's audio
// client. Its handler narrates each call through rep.
func transcribeRecordingTool(rep *Reporter, m Models) harness.Tool {
	return harness.Tool{
		Name:        transcribeTool,
		Description: "Transcribes the voice recording the user refers to, and returns its text.",
		Schema:      transcribeSchema,
		Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
			text, err := transcribe(ctx, m)
			if err != nil {
				return "", fmt.Errorf("%s: %w", transcribeTool, err)
			}
			rep.Note("go handler: %s() → %q", transcribeTool, text)
			return text, nil
		},
	}
}

// hasCode fails unless text holds the access code phraseCode, as numerals or spoken digits,
// run together or apart: "7429", "7 4 2 9", "7-4-2-9", and "seven four two nine" all pass. A
// longer run of digits holding the code, such as "17429", doesn't, and a sentence break or a
// bracket ends a run, so "7429 (four digits)" passes.
func hasCode(text string) error {
	for _, run := range digitRuns(text) {
		if run == phraseCode {
			return nil
		}
	}
	return fmt.Errorf("the reply lacks the access code %s: %q", phraseCode, text)
}

// runBreaks are the marks that end a run of digits. A comma, a hyphen, or a space joins digits,
// as in "7,429" and "seven, four, two, nine"; a sentence's end, a colon, or a bracket ends
// them.
const runBreaks = ".!?;:()[]{}\n"

// digitRuns returns each run of consecutive words in text that are all numerals or spoken
// digits, joined into one string of numerals.
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
