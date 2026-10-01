package model_test

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"testing"
	"testing/iotest"

	"github.com/JaimeStill/spike-harness-driver/model"
)

// form is a multipart request's fields and its one file.
type form struct {
	fields   map[string]string
	filename string
	file     []byte
}

func parseForm(t *testing.T, r request) form {
	t.Helper()
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" {
		t.Fatalf("Content-Type = %q (%v), want multipart/form-data", r.Header.Get("Content-Type"), err)
	}
	f := form{fields: map[string]string{}}
	mr := multipart.NewReader(bytes.NewReader(r.Body), params["boundary"])
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return f
		}
		if err != nil {
			t.Fatalf("multipart: %v", err)
		}
		data, err := io.ReadAll(p)
		if err != nil {
			t.Fatalf("multipart: %v", err)
		}
		if p.FileName() != "" {
			f.filename, f.file = p.FileName(), data
			continue
		}
		f.fields[p.FormName()] = string(data)
	}
}

func TestTranscribeWire(t *testing.T) {
	audio := bytes.Repeat([]byte("RIFFWAVE"), 10_000)
	cases := map[string]struct {
		req  model.TranscribeRequest
		want map[string]string
	}{
		"required only": {
			model.TranscribeRequest{Model: "whisper", Filename: "phrase.wav"},
			map[string]string{"model": "whisper", "response_format": "json"},
		},
		"language and prompt": {
			model.TranscribeRequest{
				Model: "whisper", Filename: "phrase.wav", Language: "en", Prompt: "Go, Pi",
			},
			map[string]string{
				"model": "whisper", "response_format": "json", "language": "en", "prompt": "Go, Pi",
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, http.StatusOK, `{"text":"hello world"}`)
			tc.req.Audio = iotest.HalfReader(bytes.NewReader(audio))
			got, err := f.client(t).Transcribe(t.Context(), tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if got.Text != "hello world" {
				t.Errorf("Text = %q", got.Text)
			}
			r := f.last(t)
			if r.Path != "/v1/audio/transcriptions" {
				t.Errorf("path = %s", r.Path)
			}
			fm := parseForm(t, r)
			if len(fm.fields) != len(tc.want) {
				t.Errorf("fields = %v, want %v", fm.fields, tc.want)
			}
			for k, v := range tc.want {
				if fm.fields[k] != v {
					t.Errorf("field %s = %q, want %q", k, fm.fields[k], v)
				}
			}
			if fm.filename != "phrase.wav" || !bytes.Equal(fm.file, audio) {
				t.Errorf("file %q of %d bytes, want phrase.wav of %d", fm.filename, len(fm.file),
					len(audio))
			}
		})
	}
}

func TestTranscribeAudioReadError(t *testing.T) {
	f := newFake(t, http.StatusOK, `{"text":""}`)
	boom := errors.New("disk gone")
	_, err := f.client(t).Transcribe(t.Context(), model.TranscribeRequest{
		Model: "w", Filename: "a.wav", Audio: iotest.ErrReader(boom),
	})
	if err == nil {
		t.Fatal("Transcribe succeeded, want the read error")
	}
}

func TestTranscribeAPIError(t *testing.T) {
	f := newFake(t, http.StatusNotFound, `{"error":{"code":"404","message":"no route"}}`)
	_, err := f.client(t).Transcribe(t.Context(), model.TranscribeRequest{
		Model: "w", Filename: "a.wav", Audio: bytes.NewReader([]byte("RIFF")),
	})
	var apiErr *model.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound || apiErr.Code != "404" {
		t.Fatalf("Transcribe = %v, want a 404 *APIError", err)
	}
}

func TestTranscribeValidation(t *testing.T) {
	f := newFake(t, http.StatusOK, `{"text":""}`)
	c := f.client(t)
	audio := bytes.NewReader(nil)
	for name, req := range map[string]model.TranscribeRequest{
		"no model":    {Audio: audio, Filename: "a.wav"},
		"no audio":    {Model: "w", Filename: "a.wav"},
		"no filename": {Model: "w", Audio: audio},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := c.Transcribe(t.Context(), req); err == nil {
				t.Fatal("Transcribe succeeded, want an error")
			}
		})
	}
	if n := f.count(); n != 0 {
		t.Fatalf("the endpoint got %d requests, want 0", n)
	}
}
