package scenario

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JaimeStill/spike-harness-driver/clutch/examples/media"
	"github.com/JaimeStill/spike-harness-driver/clutch/output"
	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/model"
)

func TestHasCode(t *testing.T) {
	for _, text := range []string{
		"The access code is 7429.",
		"The access code is 7 4 2 9.",
		"The access code is seven four two nine.",
		"Seven, four, two, nine.",
		"code: 7-4-2-9",
		"It's 7,429.",
		"seven 4 two 9",
		"The speaker says the access code is 7429 (four digits).",
		"Code: 7429\n2 speakers? No, 1.",
	} {
		if err := hasCode(text); err != nil {
			t.Error(err)
		}
	}
	for _, text := range []string{
		"", "The access code is 1234.", "17429", "74290", "seven four two", "7 4 2 9 1",
	} {
		if err := hasCode(text); err == nil {
			t.Errorf("%q passed", text)
		}
	}
}

func TestTranscribeRecordingTool(t *testing.T) {
	var got struct {
		path, model, filename string
		audio                 []byte
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		got.model = r.FormValue("model")
		f, h, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer func() { _ = f.Close() }()
		got.filename = h.Filename
		got.audio, _ = io.ReadAll(f)
		_, _ = w.Write([]byte(`{"text":"The access code is 7429."}`))
	}))
	defer srv.Close()
	c, err := model.New(model.Config{BaseURL: srv.URL + "/v1", HTTP: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	tool := transcribeRecordingTool(NewReporter(output.New(&out, &out, nil)), Models{Audio: c, AudioModel: "audio"})
	if err := tool.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := harness.ValidateSchema(tool.Schema); err != nil {
		t.Fatal(err)
	}
	text, err := tool.Handler(t.Context(), []byte(`{}`))
	if err != nil || text != "The access code is 7429." {
		t.Fatalf("handler = %q, %v", text, err)
	}
	if got.path != "/v1/audio/transcriptions" || got.model != "audio" || got.filename != "phrase.wav" || !bytes.Equal(got.audio, media.Phrase) {
		t.Errorf("request: path %s, model %q, filename %q, %d bytes of audio", got.path, got.model, got.filename, len(got.audio))
	}
	if !strings.Contains(out.String(), `go handler: transcribe_recording() → "The access code is 7429."`) {
		t.Errorf("narration:\n%s", out.String())
	}

	srv.Close()
	if _, err := tool.Handler(t.Context(), []byte(`{}`)); err == nil || !strings.HasPrefix(err.Error(), "transcribe_recording: ") {
		t.Errorf("handler against a closed endpoint = %v", err)
	}
}

func TestTheFixturesAreEmbedded(t *testing.T) {
	if !bytes.HasPrefix(media.Shapes, []byte("\x89PNG")) {
		t.Error("media.Shapes isn't a PNG")
	}
	if !bytes.HasPrefix(media.Phrase, []byte("RIFF")) || !bytes.Equal(media.Phrase[8:12], []byte("WAVE")) {
		t.Error("media.Phrase isn't a WAV")
	}
}
