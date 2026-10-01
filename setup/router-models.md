# Router models for native capabilities

Step 4's scenarios need three models that the llama.cpp router does not yet serve: one that
takes images, one that embeds text, and one that takes audio. This runbook adds them to the
Framework desktop's router. The router's configuration lives in personal-agents
(`profiles/unified-96gb.ini`, with recipes in `reference/README.md`); this spike never writes
that repository, so the architect runs these steps there.

The models were chosen on 2026-09-25 against the router's build, b10809.

| Capability | Model ID | Files | Memory |
|---|---|---|---|
| Vision, and the harness model for image sessions | `unsloth/Qwen3.8-27B-GGUF:Q4_K_XL` | `Qwen3.8-27B-UD-Q4_K_XL.gguf`, `mmproj-BF16.gguf` | 18.4 GB of weights, about 4 GiB of KV at 64k |
| Embeddings | `Qwen/Qwen3-Embedding-4B-GGUF:Q5_K_M` | `Qwen3-Embedding-4B-Q5_K_M.gguf` | about 3 GB, 2560 dimensions |
| Audio input and transcription | `ggml-org/gemma-4-E4B-it-GGUF:Q8_0` | the Q8_0 model and its BF16 mmproj | about 9 GB |

Why these:

- **Qwen3.8-27B** is the newest Qwen with vision that fits beside other models. It is dense, so
  it decodes more slowly than an A3B MoE (an estimated 10 tok/s against 50). Qwen3.8-Flash-Next
  is about 190B parameters, and its smallest usable quant is 78 GB. The faster alternative is
  `unsloth/Qwen3.6-35B-A3B-GGUF:Q4_K_XL`; avoid its `-MTP` variant, which doesn't support
  an mmproj.
- **Qwen3-Embedding-4B** has an official GGUF. It needs `last` pooling. A query reads
  `Instruct: <task>\nQuery: <text>`, and a passage is plain text.
- **Gemma 4 E4B** is the model llama.cpp's `/v1/audio/transcriptions` route was built and
  tested against, and its audio encoder is tested on Vulkan. It loops on clips longer than about
  30 seconds, so a client splits long recordings.

## Memory budget

The GPU pool is 96 GB, and personal-agents' rule keeps use to 84–88 GB. The three models above
come to about 37 GB together:

- Beside Qwen3-Coder-Next (57 GB), all three fit.
- Beside gpt-oss-120b (61 GB), they fit only if one of them is unloaded. The `audio` scenario's
  tool demo runs a Pi session on gpt-oss while calling the audio model, so for that run, unload
  the vision model.

Measure with `outpost amd usage` after each load; `ps` reports nothing useful on unified
memory.

## 1. Download

The router runs Hugging Face downloads from its `HF_HOME`, `/mnt/models/hf-cache`. Download
into the same cache:

```bash
export HF_HOME=/mnt/models/hf-cache
hf download unsloth/Qwen3.8-27B-GGUF \
  --include 'Qwen3.8-27B-UD-Q4_K_XL.gguf' 'mmproj-BF16.gguf'
hf download Qwen/Qwen3-Embedding-4B-GGUF --include '*Q5_K_M.gguf'
hf download ggml-org/gemma-4-E4B-it-GGUF --include '*Q8_0.gguf' 'mmproj*'
```

## 2. Presets

Add the recipes to `reference/README.md`, then copy them into `profiles/unified-96gb.ini`, each
with its `; Recipe:` comment:

```ini
; Recipe: ../reference/README.md, vision. Dense 27B; 16 of 64 layers hold KV
; (4 heads x 256), about 64 KB per token. The mmproj resolves from the HF cache.
[unsloth/Qwen3.8-27B-GGUF:Q4_K_XL]
c = 65536

; Recipe: ../reference/README.md, embeddings. Overrides [*]'s c. Last-token
; pooling is required, and the batch must hold the longest input.
[Qwen/Qwen3-Embedding-4B-GGUF:Q5_K_M]
embeddings = true
pooling = last
c = 8192
b = 8192
ub = 8192

; Recipe: ../reference/README.md, audio input. Clips of 30 s or less.
[ggml-org/gemma-4-E4B-it-GGUF:Q8_0]
c = 32768
```

Check each section name against `outpost server models` after the restart. The router names a
cached model `<repo>:<quant>`, and it drops unsloth's `UD-` prefix from the quant: the file
`Qwen3.8-27B-UD-Q4_K_XL.gguf` is served as `unsloth/Qwen3.8-27B-GGUF:Q4_K_XL`. A request that
names the model with the prefix fails with `model '…' not found`.

If an image or audio encoder misbehaves on Vulkan, add `mmproj-offload = false` to its section
to run the encoder on the CPU.

## 3. Install and load

```bash
outpost preset install unified-96gb
outpost service restart
outpost server models
outpost server models load unsloth/Qwen3.8-27B-GGUF:Q4_K_XL
outpost server models load Qwen/Qwen3-Embedding-4B-GGUF:Q5_K_M
outpost server models load ggml-org/gemma-4-E4B-it-GGUF:Q8_0
outpost amd usage
```

## 4. Smoke tests

Run these from the spike's checkout, with `LLAMA_BASE_URL` set as it is for `clutch`. The
fixtures are in `clutch/examples/media` (see its README). The commands pass base64 to jq through
`--rawfile` because an audio clip is too long for a command-line argument.

The `/v1/models` response should list image input for the vision model and audio input for the
audio model. Pi reads the same field to decide whether a model takes images:

```bash
curl -s "$LLAMA_BASE_URL/v1/models" \
  | jq -r '.data[] | [.id, (.architecture.input_modalities | join(","))] | @tsv'
```

Vision:

```bash
base64 -w0 clutch/examples/media/shapes.png \
| jq -n --rawfile img /dev/stdin '{
  model: "unsloth/Qwen3.8-27B-GGUF:Q4_K_XL",
  messages: [{role: "user", content: [
    {type: "text", text: "Name each shape in this image and its color."},
    {type: "image_url", image_url: {url: ("data:image/png;base64," + $img)}}]}]}' \
| curl -s "$LLAMA_BASE_URL/v1/chat/completions" -H 'Content-Type: application/json' -d @- \
| jq -r '.choices[0].message.content'
```

Embeddings (expect `2560`):

```bash
curl -s "$LLAMA_BASE_URL/v1/embeddings" -H 'Content-Type: application/json' -d '{
  "model": "Qwen/Qwen3-Embedding-4B-GGUF:Q5_K_M",
  "input": ["Instruct: Given a question, retrieve passages that answer it\nQuery: How do bees make honey?"]
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
