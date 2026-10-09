# Router models

clutch's defaults on the llama.cpp target are the four models of personal-agents' **set A**,
loaded side by side on the Framework desktop's router: a harness model, and one model each for
vision, embeddings, and audio. The router's configuration lives in personal-agents; this spike
never writes that repository. Its presets are in `profiles/unified-96gb.ini`, and why each model
and its context was chosen is in `reference/model-tiers.md` ("~90-96GB (unified memory)").

The set runs on the router's llama.cpp build, b11529.

| Role | Model ID | clutch default for |
|---|---|---|
| Harness model: text only, tool calls | `ggml-org/gpt-oss-120b-GGUF:MXFP4` | `--model` on Pi and OpenCode over `llama.cpp` |
| Vision: text and image input | `ggml-org/gemma-4-26B-A4B-it-GGUF:Q4_0` | `--vision-model`, and `--harness-vision-model` on Pi and OpenCode |
| Embeddings: 768 dimensions, mean pooling | `ggml-org/embeddinggemma-2-GGUF:Q8_0` | `--embed-model` |
| Audio: text, image, and audio input | `ggml-org/gemma-4-E4B-it-GGUF:Q8_0` | `--audio-model`, heard in chat as well as transcribed |

What clutch relies on in each:

- **gpt-oss-120b takes no images.** The vision scenario's last step sends an image to a session
  on the harness model to show the harness dropping it, so the harness default must stay text
  only.
- **Gemma 4 reasons before it answers.** A direct-client call capped at 200 completion tokens
  came back with empty content, the reasoning having used the cap. clutch's vision and audio
  calls set no cap; a caller that sets one leaves room for the reasoning (2000 was enough).
- **EmbeddingGemma 2 takes prompt forms.** For question answering, its model card gives the
  query as `task: question answering | query: {question}` and a document without a title as
  `title: none | text: {passage}`. The embed scenario sends both. A model trained without them,
  such as Azure's `text-embedding-3-small`, embeds the prefix as more text.
- **Gemma 4 E4B** is the model llama.cpp's `/v1/audio/transcriptions` route was built and
  tested against. It loops on clips longer than about 30 seconds, so a client splits long
  recordings.

## Memory

The four fit together, all loaded at once, with four requests in flight on each:
personal-agents' `reference/memory-footprint.md` ("Set A, measured") holds the measurement,
which leaves about 3.3 GiB of the 96 GiB pool free, over its 3 GiB bar. No scenario needs a
model unloaded.

## Load

The router loads a model on its first request, which can take minutes, and `/models` lists
each model's status. To load one ahead of a run:

```bash
curl -s "$LLAMA_BASE_URL/models" | jq -r '.data[] | [.id, .status.value] | @tsv'
curl -s "$LLAMA_BASE_URL/models/load" -H 'Content-Type: application/json' \
  -d '{"model": "ggml-org/gemma-4-26B-A4B-it-GGUF:Q4_0"}'
```

## Smoke tests

Run these from the spike's checkout, with `LLAMA_BASE_URL` set as it is for `clutch`. The
fixtures are in `clutch/examples/media` (see its README). The commands pass base64 to jq through
`--rawfile` because an audio clip is too long for a command-line argument. `clutch scenario
vision`, `embed`, and `audio` run the same checks through the code.

The `/v1/models` response should list image input for the two Gemma 4 models and audio input for
E4B. Pi reads the same field to decide whether a model takes images:

```bash
curl -s "$LLAMA_BASE_URL/v1/models" \
  | jq -r '.data[] | [.id, (.architecture.input_modalities | join(","))] | @tsv'
```

Vision:

```bash
base64 -w0 clutch/examples/media/shapes.png \
| jq -n --rawfile img /dev/stdin '{
  model: "ggml-org/gemma-4-26B-A4B-it-GGUF:Q4_0",
  messages: [{role: "user", content: [
    {type: "text", text: "Name each shape in this image and its color."},
    {type: "image_url", image_url: {url: ("data:image/png;base64," + $img)}}]}]}' \
| curl -s "$LLAMA_BASE_URL/v1/chat/completions" -H 'Content-Type: application/json' -d @- \
| jq -r '.choices[0].message.content'
```

Embeddings (expect `768`):

```bash
curl -s "$LLAMA_BASE_URL/v1/embeddings" -H 'Content-Type: application/json' -d '{
  "model": "ggml-org/embeddinggemma-2-GGUF:Q8_0",
  "input": ["task: question answering | query: How do bees make honey?"]
}' | jq '.data[0].embedding | length'
```

Transcription (expect the phrase in `clutch/examples/media/README.md`):

```bash
curl -s "$LLAMA_BASE_URL/v1/audio/transcriptions" \
  -F file=@clutch/examples/media/phrase.wav \
  -F model=ggml-org/gemma-4-E4B-it-GGUF:Q8_0 \
  -F response_format=json | jq -r .text
```

Audio in chat:

```bash
base64 -w0 clutch/examples/media/phrase.wav \
| jq -n --rawfile wav /dev/stdin '{
  model: "ggml-org/gemma-4-E4B-it-GGUF:Q8_0",
  messages: [{role: "user", content: [
    {type: "text", text: "What access code does the speaker say?"},
    {type: "input_audio", input_audio: {data: $wav, format: "wav"}}]}]}' \
| curl -s "$LLAMA_BASE_URL/v1/chat/completions" -H 'Content-Type: application/json' -d @- \
| jq -r '.choices[0].message.content'
```
