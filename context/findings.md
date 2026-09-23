# Findings

Evidence toward the spike's question, by part. The coordinator's `plan` session reads it with
the result. The package documentation in `harness`, `harness/stdio`, and `pi` explains how the
built code works; this note holds what the code shows about harnesses in general.

## Exchanges

- Pi's RPC events carry no request ID. Only command responses echo the client's `id`. An
  exchange is therefore the window from `agent_start` to `agent_settled`, and a session carries
  one exchange at a time. `agent_end` doesn't end an exchange, because a retry, a compaction, or
  a queued message can start another run after it.
- A `prompt` response means only that Pi accepted the prompt. The run's outcome, including any
  error, arrives as events.
- Pi answers `abort` after the run settles. Pi keeps queued messages through an abort, so a
  cancellation sends `clear_queue` first. Pi cancels in two round trips, and the exchange can end
  between them, so a cancellation in flight holds off the next exchange.
- The driver assigns exchange IDs itself (UUIDv7), because the harness has none to offer.

## Sessions

- Pi's `get_state` gives the session ID. Under `--no-session` the session is in memory only;
  persistence is step 2's question.

## Capabilities

- gpt-oss-120b on the llama.cpp router streams thinking deltas, although Pi's model catalog marks
  it `reasoning: false`.
- With the router's prompt cache warm, Pi reports an input usage of 1 token. `payloads.md` covers
  the consequence.

## Infrastructure shape

This design is provisional until the Claude Code and OpenCode adapters (step 5) use it.

- Three layers:
  - `harness` is transport-agnostic. It holds exchange scoping, sequencing, result folding, and
    cancellation, over a four-method `Connection`.
  - `harness/stdio` is the transport for line-protocol harnesses.
  - `pi` is the adapter. It is about 300 lines of Pi-specific code, against about 1,000 lines
    of reusable layers.
- Assumes Claude Code (`stream-json` with control requests) and OpenCode (`opencode acp`) fit
  `stdio` as Pi does. `adapters.md` holds the plan.

## Process lifecycle

- A driver that runs a harness as a process owns everything the harness starts. The harness runs
  in a process group of its own, and what is left of the group is killed when the harness exits.
  `service-runtime.md` covers the rejected alternatives.
- The harness's stdout and stderr must be `*os.File` pipes. Any other writer makes exec copy it
  through a goroutine that `Wait` waits for, and a leftover child holding the pipe then holds up
  the exit.
- Line protocols are split on LF only. A JSON string may hold U+2028, and a line may exceed any
  fixed buffer.
