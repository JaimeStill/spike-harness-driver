package model

import (
	"context"
	"errors"
	"io"
	"mime/multipart"
)

// TranscribeRequest asks for the transcript of an audio file.
type TranscribeRequest struct {
	Model string
	// Audio is the file's content, streamed as it is sent.
	Audio io.Reader
	// Filename is the file's name. The endpoint infers the audio's format from its extension.
	Filename string
	// Language, when set, is the audio's language as an ISO-639-1 code, such as "en".
	Language string
	// Prompt, when set, guides the transcript's style or spelling.
	Prompt string
}

// Transcript is an audio file's transcript.
type Transcript struct {
	Text string
}

// Transcribe returns the transcript of req's audio. The request is checked before it is sent.
//
// The audio streams into the request body as the request is sent, rather than being read into
// memory first, since a recording can be large. Transcribe returns once the response is read,
// and a Reader that blocks past then holds only the goroutine copying it.
func (c *Client) Transcribe(ctx context.Context, req TranscribeRequest) (Transcript, error) {
	switch {
	case req.Model == "":
		return Transcript{}, errors.New("model: transcribe: the request names no model")
	case req.Audio == nil:
		return Transcript{}, errors.New("model: transcribe: the request has no audio")
	case req.Filename == "":
		return Transcript{}, errors.New("model: transcribe: the request has no filename")
	}
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() { _ = pw.CloseWithError(writeForm(mw, req)) }()
	// Closing the read side, whatever became of the request, fails the writer's next write,
	// so it can't wait forever on a body no one reads.
	defer func() { _ = pr.Close() }()

	var reply struct {
		Text string `json:"text"`
	}
	err := c.post(ctx, "transcribe", "audio/transcriptions", mw.FormDataContentType(), pr, &reply)
	if err != nil {
		return Transcript{}, err
	}
	return Transcript{Text: reply.Text}, nil
}

// writeForm writes req as the multipart form a transcription takes.
func writeForm(mw *multipart.Writer, req TranscribeRequest) error {
	fields := [][2]string{
		{"model", req.Model},
		{"language", req.Language},
		{"prompt", req.Prompt},
		{"response_format", "json"},
	}
	for _, f := range fields {
		if f[1] == "" {
			continue
		}
		if err := mw.WriteField(f[0], f[1]); err != nil {
			return err
		}
	}
	part, err := mw.CreateFormFile("file", req.Filename)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, req.Audio); err != nil {
		return err
	}
	return mw.Close()
}
