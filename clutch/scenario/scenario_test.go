package scenario_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/clutch/domain/session"
	"github.com/JaimeStill/spike-harness-driver/clutch/output"
	"github.com/JaimeStill/spike-harness-driver/clutch/scenario"
	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/harness/filestore"
	"github.com/JaimeStill/spike-harness-driver/internal/harnesstest"
	"github.com/JaimeStill/spike-harness-driver/model"
)

func reporter() (*scenario.Reporter, *bytes.Buffer) {
	var out bytes.Buffer
	return scenario.NewReporter(output.New(&out, &bytes.Buffer{}, nil)), &out
}

func TestRunNarratesStepsAndCleansUp(t *testing.T) {
	var ran []string
	cleaned := false
	s := scenario.Scenario{
		Name: "demo",
		Steps: func() ([]scenario.Step, func() error) {
			step := func(name string, err error) scenario.Step {
				return scenario.Step{Intent: name, Action: func(context.Context, *scenario.Reporter) error {
					ran = append(ran, name)
					return err
				}}
			}
			return []scenario.Step{step("one", nil), step("two", errors.New("boom")), step("three", nil)},
				func() error { cleaned = true; return nil }
		},
	}
	rep, out := reporter()
	err := scenario.Run(t.Context(), s, rep)
	if err == nil || !strings.Contains(err.Error(), "demo: step 2: boom") {
		t.Fatalf("Run = %v", err)
	}
	if strings.Join(ran, ",") != "one,two" || !cleaned {
		t.Errorf("ran %v, cleaned %v", ran, cleaned)
	}
	if !strings.Contains(out.String(), "[1/3] one") || !strings.Contains(out.String(), "[2/3] two") {
		t.Errorf("narration:\n%s", out.String())
	}
}

func TestRunStopsAtAFailedNeed(t *testing.T) {
	built := false
	s := scenario.Scenario{
		Name:  "demo",
		Needs: []scenario.Need{{What: "a harness", Check: func(context.Context) error { return errors.New("missing") }}},
		Steps: func() ([]scenario.Step, func() error) { built = true; return nil, nil },
	}
	rep, _ := reporter()
	err := scenario.Run(t.Context(), s, rep)
	if err == nil || !strings.Contains(err.Error(), "need a harness: missing") || built {
		t.Errorf("Run = %v, built = %v", err, built)
	}
}

// stubModels returns Models over an endpoint that answers every request the capability
// scenarios make as a model that got it right would: a chat naming the shapes or the code
// depending on what the request carries, embeddings that rank the honey passage first, and the
// recording's transcript.
func stubModels(t *testing.T) func() (scenario.Models, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/chat/completions":
			answer := "I can't tell."
			switch {
			case bytes.Contains(body, []byte(`"image_url"`)):
				answer = "A red circle, a blue square, and a green triangle."
			case bytes.Contains(body, []byte(`"input_audio"`)):
				answer = "The speaker says the access code is seven four two nine."
			}
			reply, _ := json.Marshal(map[string]any{
				"choices": []any{map[string]any{"message": map[string]any{"content": answer}, "finish_reason": "stop"}},
				"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
			})
			_, _ = w.Write(reply)
		case "/v1/embeddings":
			var req struct {
				Input []string `json:"input"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			// One axis per topic, so each text lies along the topic it mentions.
			var data []any
			for i, in := range req.Input {
				v := []float32{0.1, 0.1, 0.1}
				switch {
				case strings.Contains(in, "honey"):
					v[0] = 1
				case strings.Contains(in, "Volcanoes"):
					v[1] = 1
				case strings.Contains(in, "Stock"):
					v[2] = 1
				}
				data = append(data, map[string]any{"index": i, "embedding": v})
			}
			reply, _ := json.Marshal(map[string]any{"data": data, "usage": map[string]any{"prompt_tokens": 40}})
			_, _ = w.Write(reply)
		case "/v1/audio/transcriptions":
			_, _ = w.Write([]byte(`{"text":"The access code is 7429."}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := model.New(model.Config{BaseURL: srv.URL + "/v1", HTTP: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	m := scenario.Models{
		Target:        "stub",
		Chat:          c,
		Audio:         c,
		VisionModel:   "vision",
		EmbedModel:    "embed",
		AudioModel:    "audio",
		AudioInChat:   true,
		HarnessVision: "harness-vision",
	}
	return func() (scenario.Models, error) { return m, nil }
}

func TestScenariosOverAStubHarness(t *testing.T) {
	store := filestore.New(t.TempDir())
	var opened []harness.Options
	svc := session.New(
		func() (harness.Driver, error) {
			return harnesstest.Driver{Stream: "essay", Opened: func(o harness.Options) { opened = append(opened, o) }}, nil
		},
		func() harness.Options { return harness.Options{Store: store} },
	)
	// The stub harness replies with the same text whatever it is asked, calls no tools, and
	// gives no structured response, so the scenarios that check the model's reply fail at
	// their first check.
	fails := map[string]string{
		"tool":       "step 2: the model never called the lookup_code tool",
		"skill":      "step 1: the reply lacks the motto",
		"structured": "step 2: exchange ended in error: " + harness.ErrNoStructuredResponse.Error(),
		"vision":     "step 1: the reply lacks the red circle, blue square, green triangle",
		// Steps 1 and 2 go to the stub endpoint, which answers them correctly.
		"audio": "step 3: the model never called the transcribe_recording tool",
	}
	scenarios := scenario.Scenarios(svc, stubModels(t), scenario.Needs{})
	for _, s := range scenarios {
		t.Run(s.Name, func(t *testing.T) {
			opened = nil
			rep, out := reporter()
			cmd := scenario.Command(s, func() *scenario.Reporter { return rep })
			cmd.SetArgs(nil)
			err := cmd.ExecuteContext(t.Context())
			if want, ok := fails[s.Name]; ok {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("Run = %v, want an error containing %q\n%s", err, want, out.String())
				}
			} else if err != nil {
				t.Fatalf("%v\n%s", err, out.String())
			}
			if s.Name == "embed" {
				// The embed scenario drives no harness: the harness has nothing for embeddings.
				if len(opened) != 0 || !strings.Contains(out.String(), "1. honey") {
					t.Errorf("opened %d sessions; narration:\n%s", len(opened), out.String())
				}
				return
			}
			if !strings.Contains(out.String(), "session "+harnesstest.SessionID) || !strings.Contains(out.String(), "result: stop=") {
				t.Errorf("narration:\n%s", out.String())
			}
			if len(opened) == 0 {
				t.Fatal("no session opened")
			}
			switch first := opened[0]; s.Name {
			case "tool":
				if len(first.Tools) != 1 || first.Tools[0].Name != "lookup_code" || first.HarnessTools == nil || len(first.HarnessTools) != 0 {
					t.Errorf("opened with tools %+v, harness tools %#v", first.Tools, first.HarnessTools)
				}
			case "skill":
				if len(first.Skills) != 1 || first.Skills[0].Name != "clutch-motto" || first.HarnessTools == nil || len(first.HarnessTools) != 0 {
					t.Errorf("opened with skills %+v, harness tools %#v", first.Skills, first.HarnessTools)
				}
			case "structured":
				if len(first.Tools) != 0 || first.HarnessTools == nil || len(first.HarnessTools) != 0 {
					t.Errorf("opened with tools %+v, harness tools %#v", first.Tools, first.HarnessTools)
				}
				// The store records each exchange's request, so it shows the schema was sent.
				recs, err := store.Records(t.Context(), harnesstest.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				if !slices.ContainsFunc(recs, func(r harness.Record) bool {
					return json.Valid(r.Request.Schema) && strings.Contains(string(r.Request.Schema), `"city"`)
				}) {
					t.Errorf("no recorded request carries the capital schema: %+v", recs)
				}
			case "vision":
				if first.Model != "harness-vision" || first.HarnessTools == nil || len(first.HarnessTools) != 0 {
					t.Errorf("opened on %q with harness tools %#v", first.Model, first.HarnessTools)
				}
				// The record keeps each image's media type, which shows the image was sent.
				recs, err := store.Records(t.Context(), harnesstest.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				if !slices.ContainsFunc(recs, func(r harness.Record) bool {
					return len(r.Request.Images) == 1 && r.Request.Images[0].MediaType == "image/png"
				}) {
					t.Errorf("no recorded request carries the image: %+v", recs)
				}
			case "audio":
				if len(first.Tools) != 1 || first.Tools[0].Name != "transcribe_recording" || first.Model != "" ||
					first.HarnessTools == nil || len(first.HarnessTools) != 0 {
					t.Errorf("opened on %q with tools %+v, harness tools %#v", first.Model, first.Tools, first.HarnessTools)
				}
				for _, want := range []string{`transcript: "The access code is 7429."`, "seven four two nine"} {
					if !strings.Contains(out.String(), want) {
						t.Errorf("narration lacks %q:\n%s", want, out.String())
					}
				}
			}
		})
	}

	var listing bytes.Buffer
	scenario.WriteListing(&listing, scenarios)
	for _, name := range []string{"exchange", "cancel", "resume", "tool", "skill", "structured", "vision", "embed", "audio"} {
		if !strings.Contains(listing.String(), name) {
			t.Errorf("listing lacks %s:\n%s", name, listing.String())
		}
	}
}
