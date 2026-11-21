# Design

The reasoning behind the root module's packages, the MCP bridge, and the harness adapters: what
each unexported part does and why it is built that way. Each package's own documentation, which
`go doc` prints, gives its API and points to its section here.

## The session

Package [`harness`](../harness/).

**Cancelling.** A Session gives each Exchange its cancellation hook, `cancelHook`, which calls
`Session.cancel`. That cancels only while the exchange's run is under way: once its EventEnded
has arrived (`runEnded`, set while the ended exchange is recorded), a cancellation would reach an
idle harness or the next exchange's run. A harness may take several round trips to cancel, and
the exchange may end during them, so `cancelling` holds off the next Send until the cancellation
finishes; it is nil when none is in flight. Each Connection.Cancel is bounded by `cancelTimeout`.
An exchange is marked cancelled as the cancellation starts, because it owes no structured
response once cancelled, whether or not the harness reports the cancellation in an event of its
own, as it may not when the cancel lands during a tool call. A Connection.Cancel error that
Close cut short is no error of the exchange's.

**Routing.** `Session.route` hands each of the Connection's events to the open exchange, and ends
that exchange when the Connection's events close; `eventsClosed` closes then, and Close waits for
it. An event outside any exchange, such as a harness's queue notice after a cancelled run ends,
is dropped. An exchange is recorded when its EventEnded arrives, while it is still the open
exchange, so no Send can start another run that would append to its entries. An exchange whose
schema went unanswered (`Exchange.unanswered`: a schema, a run that ended neither cancelled nor
in an error, and no structured response) gets ErrNoStructuredResponse first, so its record
carries it. A failure to record becomes the exchange's error. When the harness exits during an
exchange, the exchange is recorded without entries, since the harness is gone, and fails with
the exit error.

**Recording.** `Session.record` binds the exchange to the journal entries appended since the
cursor (`bind`), advances the cursor, and stores the record (`put`). `journal` is nil when the
Connection keeps no Journal, or there is no Store. `cursor` is the last journal entry bound to an
exchange; when binding fails, as when Close or the harness's exit cuts the journal call short,
the exchange is still recorded, without entries and with the binding error, and `resync` marks
the cursor stale. The next Send then takes the cursor from the journal's head (`resyncCursor`)
before it prompts, so the head is where the new exchange's entries begin. Binding and storing
are each bounded by `recordTimeout`. A record outlives Close, which can end an exchange as it is
recorded, so `put` runs under a context bounded by `recordTimeout` alone. `Session.verify`, at
setup, checks that the journal still holds the last entry the store recorded for the session.
A record error stands in for the exchange's error only when the exchange has none of its own.

**The exchange.** `Exchange.push` stamps each event with the exchange's IDs and next sequence
number, folds it into the result, and queues it, under the exchange's lock, so events pushed from
different goroutines keep Seq order. Events after EventEnded are dropped. `watch` cancels the
exchange when the context it was sent with is done, until it ends; `unwatch` stops that. A
recorded request keeps each image by its media type alone (`recorded`), so a Store holds no image
data whatever it does with the Record.

**Validation.** `toolName` is the rule the model providers share for a tool's name.
`validateImages` checks that each image has a media type and data. It does not check whether
the model takes images, which only the harness knows: Pi, for one, replaces an image with a note
for a model without image input.

**The tests.** `fakeConnection` is a Connection the test scripts: it records prompts and
cancellations, the test emits the harness's events by hand, `promptGate` and `cancelGate` hold
Prompt's and Cancel's answers until they close or the call's context ends (a gate that never
closes is a harness that never answers), and `exitErr` is why the harness exited. `never`
checks that a channel receives nothing for a while, for a thing that must not happen and would
happen on another goroutine if it did. `holdPuts` holds every Put until released; a test defers
the release before the session's Close, so a failing assertion can't leave Close waiting on a
Put. The cases pin the reasoning above: a run that ends in an error is the run's error, and the
harness's exit later is the Connection's to report; a cancellation of an exchange that has ended
must not reach the Connection, or it would stop the open exchange; when Send's context ends
while the harness has the prompt but hasn't answered, Send still waits for the answer and then
cancels the run it started, so no run is left without an exchange; and after a binding fails,
the next exchange claims only its own entry, not the first's unbound ones.

## The line protocol

Package [`harness/stdio`](../harness/stdio/).

**The process.** A line-protocol harness shuts down at the end of its input, so cancelling the
command closes stdin rather than killing, and returns ErrProcessDone so Wait reports the exit
status as it is: a clean exit after Close is no error. Stdout and stderr are `*os.File` pipes,
which exec hands to the harness as they are. Any other writer would be copied through goroutines
that Wait waits for, so a process the harness left holding a pipe would hold up Wait, and with it
the cleanup of that process. The harness holds its own copies of the write ends; the driver closes
its copies after Start, so the readers reach EOF once every process that holds them is gone.

`Process.wait` is the only caller of `cmd.Wait`, which returns as soon as the harness exits. It
then kills the processes the harness left in its group, and closes the pipes after WaitDelay if a
process outside the group still holds them, so the readers always end. `stdoutDone` and
`stderrDone` close once each reader has stopped, `waited` once `cmd.Wait` has returned and
`exitErr` is set, and `done` once the process has exited and Lines has closed. `cause` is why the
process was asked to stop, as it stood when the process exited, so a Close that comes after the
harness exited on its own doesn't recast the exit as asked for. A Process keeps the last
`stderrTail` bytes of the harness's stderr for its exit error.

The reader splits lines on LF only: a JSON string may hold U+2028, and a line may exceed any fixed
token size. A read that fails with `os.ErrClosed` means `wait` closed the pipe after the harness
exited: an end of output, not a failure.

**The process group.** On Unix, `ownGroup` starts the harness as the leader of a process group of
its own, so `killGroup` can reach every process the harness starts, such as its tool processes,
once the harness has exited: processes it started and didn't stop, which would otherwise be
orphaned. The harness has been reaped by then, so if its group is already empty, its PID is free.
A new process that took that PID and made itself a group leader in the moment between the reaping
and the kill would be killed instead. That needs the PIDs to wrap around within microseconds, and
the risk is accepted. Where process groups don't exist, both do nothing, and a harness's leftover
children outlive it.

**The client.** `calls` correlates commands with their responses by id, whatever order the
responses arrive in. Each call's channel receives exactly one Response: the harness's, or the
`failure` that ended the calls, which every later call also fails with. A response to no open
call is dropped. `fail` never blocks on a send: an open call's channel is still empty, since
`resolve` removes a call before it delivers.

The Client's events queue without bound, so a harness that emits events while no one is reading
them, as during a start-up handshake, can't stall the responses behind them. The queue lives as
long as the process: Close discards what no one read. `answer` has the Handler answer each of the
harness's requests on a goroutine of its own; an answer the harness can no longer take, because
it has exited, is dropped. `answering` counts the requests being answered. Once the harness has
exited, `shutdown` fails the open calls with the exit error, ends the answers' context, waits a
bounded time for the answers in progress, and closes Events; Err reports the exit to whoever
reads them. A handler should return once its context ends. One that doesn't is abandoned after
the process's wait delay, so it can't hold the Client open; its late answer goes nowhere.

**The tests.** The test binary is its own line-protocol peer, selected by `peerEnv`: "echo"
answers commands, each on its own goroutine after a delay that reverses their order, so responses
arrive out of order; "stubborn" also ignores the end of its input; "orphaner" leaves a child
holding its stdout when its input ends; and "sleep" is that child. The peer writes U+2028 raw, as
a harness written in another language may (encoding/json escapes it); a splitter that treats it
as a line break, as Node's readline does, would cut the line in two. `noRaceExitSleep` keeps a
race-enabled peer from sleeping a second at exit. One test's answers each wait for every request
to be asked, so it deadlocks unless the client answers requests concurrently. The orphan tests
run only on Linux, where `alive` can tell a zombie from a live process through `/proc`: a killed
process its new parent hasn't reaped yet is a zombie, which counts as gone. A detached orphan
leaves the peer's process group, as a daemonizing process would, so the group kill can't reach
it, and its pipes close after the wait delay. `trackOrphan` kills the orphan when the test ends
either way, so no test leaves a process behind. The tests' wait delay is long, so a test that
finishes well within it shows the wait delay wasn't needed.

## Skills and command tools

Package [`harness/catalog`](../harness/catalog/).

**Skills.** `skillName` is the Agent Skills rule for a skill's name, which Pi follows: lowercase
letters and digits in hyphen-separated runs, with no leading, trailing, or doubled hyphen. The
length limit, 64, is checked apart. `fs.ReadDir` returns the entries sorted by name, and each
skill's name is its entry's, so Skills needs no sort.

`frontmatter` parses the YAML frontmatter at the start of a skill's file: the lines between a
first line of `---` and the next line of `---`. It reads only the top-level `key: value` lines a
skill's name and description take, with the value bare or in single or double quotes, and skips
comments and indented lines. A full YAML parser would be the module's first dependency, for two
keys. A block scalar (`|` or `>`) for name or description is refused rather than misread,
because its value is on the indented lines this parser skips; under any other key it is ignored,
since any other key may take a form this parser doesn't without making the skill unreadable.
`scalar` returns the value of a one-line scalar: the text inside its quotes, or, bare, the text
before any comment. Escapes inside double quotes are left as written.

**Command tools.** A call's stdout, the text the model receives, is bounded by `maxOutput`;
`limited` keeps what is written to it up to its bound and fails the write that would pass it,
which ends `drain`'s copy. `drain` then closes the pipe, so the command's next write fails on the
closed pipe and a runaway command stops rather than blocking on a full one. A failed call's error
carries the last `stderrTail` bytes of its stderr (`tail`), where its reason usually is.
`manifest.validate` checks the manifest's own fields; the tool it describes is checked as any
harness.Tool is, by Validate.

`command.run` gives the command its stdout and stderr as `*os.File` pipes, so Wait returns as
soon as it exits rather than when every process holding the pipes lets go, and closes the
driver's copies of the write ends so the readers reach EOF once every process holding them is
gone. On Unix the command runs in a process group of its own (`ownGroup`), cancellation kills the
whole group, and `killGroup` kills what is left of it once the command exits; the group is gone
already when the command left nothing behind. A tool is often a script, and killing only the
script would leave the processes it started running, holding its stdout open. So no process a
script started outlives the call: the driver owns what a tool leaves behind, as it owns what a
harness does. A process that left the group can still hold the pipes, so reading stops after
`waitDelay`. Where process groups don't exist, cancellation kills the command alone, and the wait
delay stops the reading of output a leftover process still holds.

**The tests.** The command-tool tests skip on Windows, where the test tools' shell scripts don't
run. On Unix the test tools record the PID of the child they start, and the check sends signal 0,
which tests for the process without touching it; a killed child that hasn't been reaped yet is a
zombie, which still answers, so the check gives the reaper a moment. Cancelling a call kills the
script's child well within the wait delay, and a command that exits leaving a child holding its
output answers at once.

## The skill cache

Package [`harness/cache`](../harness/cache/).

Only the current user writes under the root, so an existing entry whose tree digest doesn't
match its name is damage, such as a copy a crash cut short, and the entry is removed and written
again. Losing the race to
another writer of the same content leaves the directory that writer wrote. `hashFile` adds each
file to a tree's digest length-prefixed, so no two trees hash alike by moving bytes between files.

## The record files

Package [`harness/filestore`](../harness/filestore/).

`maxRecord` bounds one record's line, which holds a whole request and result. A session ID must
name a file in the store's directory and nothing beyond it (`path`).

## The model client

Package [`model`](../model/).

The request body never carries max_tokens or temperature, which reasoning models reject, nor
stream, which this client doesn't read. A message whose content is a single Text goes on the wire
as a string, the form every endpoint and chat template takes, and any other content as an array
of parts.

New encodes the query once, from a copy, so a caller changing its url.Values after New can't
change the requests. `endpoint` appends a route to the base path with exactly one slash between
them. `post` turns a non-2xx response into an `*APIError`, and closes the body when it is an
io.Closer and no request takes it, so a writer feeding it through a pipe is released. A
transcription's form is written through a pipe whose read side is closed whatever became of the
request, which fails the writer's next write, so it can't wait forever on a body no one reads.
An embedding carries its input's index, and an endpoint that batches may return them out of
order, so Embed places each by its index.

`maxErrorBody` bounds how much of an error response is read. It is a safety bound against an
endpoint that streams an unbounded error body, not a tuning choice: an error message fits many
times over. A failed read keeps the part of the body that was read, which is still the best
account of the error there is. `maxErrorText` bounds how much of an unparsed body Error prints.
Endpoints send an error code as a string or a number; `code` takes either as text.

In the tests, the server notices the client hanging up only once the body has been read, and
cleanups run last first, so the handler is released before the server's Close waits on it.

## The workflow runner

Package [`workflow`](../workflow/).

**Runs.** Each run's loop is counted in the Runner's `wg`, added under the Runner's lock while it
isn't closed, so no Add races Shutdown's Wait. `newRun` checks and registers a run under one hold
of that lock, so two Resumes of one run can't both pass. A run that never launched is dropped,
closing any subscriber that attached meanwhile. `slots` holds a token per exchange in flight, and
is nil when there is no limit. A run's loop takes the slot before it launches a step
(`tryAcquire`), so its steps start in declaration order and none opens a session while it would
only wait for a slot. Releasing a slot wakes every run, since any of them may have a step waiting
for one.

A run's `ctx` is its lifetime, which Cancel and Shutdown end with their causes. Its `steps`
context is what the steps run under: a failed step ends it, which cancels the others still in
flight while the run goes on to log its failure. `failure` is the first step failure, which ends
the run. `interruption` is why the run stopped without logging an end: a Shutdown, or a log that
failed. Cancel doesn't cancel a run already stopping for another reason, a Shutdown: the next
runner resumes it. `sessions` are the open harness sessions by the workflow's session name;
`busy` marks the sessions a step holds, and `inflight` the steps with a goroutine.

**The log.** `run.log` numbers an event, appends it to the Store, folds it into the state, and
delivers it to the subscribers. It folds into a copy, because Apply writes into the maps and a
failed append must not leave them changed. A failure to append interrupts the run, since its log
no longer holds the event. Each event is appended under a context of its own, bounded by
`logTimeout`, so a cancelled run still logs how it ended. A subscriber that went away leaves its
queue behind, and `log` drops it. Subscribe reads the logged events and registers the subscriber
under the run's lock, which every logged event takes, so no event falls between the replay and the
live stream. A subscriber's `queue` holds any number of events, so a run never waits on a slow
subscriber and never drops an event for one. A queue stops delivering once its subscriber's
context ends, or once it is closed and drained; whatever is still queued then is discarded, and
later pushes are dropped.

**The loop.** `run.loop` schedules the run's steps until every step is done, or the run is
cancelled, fails, or is interrupted, and then ends it. Once the run stops, the loop waits only for
its steps to release. `ready` returns the steps that can start now, in declaration order: not
done, without a goroutine, every step they come after done, and their session free. A step the
log has as running but that has no goroutine is one a resume or a usage limit left to run again.
When nothing runs and nothing can start, yet the run isn't done, the run fails with "no step can
start"; otherwise the loop would wait for a wake that never comes. A failure was decided before
any Shutdown, so the run ends failed even when interrupted; only a log that can't be appended to
keeps it from ending. `settle` gives a resumed run the outcome its log already decided: a failed
step fails the run, and a cancelled step cancels it, once its steps in flight have ended.

**Steps.** `run.step` runs one step in the slot the loop took for it: it opens the step's session
if it isn't open, and either adopts the step's exchange from the session's records or sends its
prompt and follows the exchange to its end. The step holds its session as busy, so no other step
opens it meanwhile. `adopt` serves a step whose exchange the log has as started but not ended: an
exchange that ended, neither cancelled nor in an error, before the runner last stopped becomes the
step's result, and the step is logged as done. A stop reason is what tells a finished run from one
that ended without a final message, as a cancellation can, so an exchange without one runs again.
`end` logs how a step ended: cancelled when the steps' context ended first; a usage limit pauses
the run and leaves the step to run again, for `limitRetry` when the harness gives no reset time;
any other error fails the run. An interrupted run logs nothing, so a resume takes the step up.
`release` frees the step's session and wakes the loop. A session none of whose steps is left to
run closes, which ends its harness process, before the step leaves `inflight`: the loop finishes
only once `inflight` is empty, so Shutdown and Wait never return ahead of a closing harness.

**The tests.** `fakeHarness` opens sessions over scripted connections, keeps their exchange
records, and counts the prompts in flight; a script's `slowCancel` holds a cancellation's end, as
a harness's cancel round trips do. The crash test has the first runner's log refuse a step's end:
the run stops as a crash would, after the step's exchange ended and the harness recorded it, but
before the log said so, and the resumed run adopts the exchange.

## The run logs

Package [`workflow/filestore`](../workflow/filestore/).

`maxEvent` bounds one event's line: a run_started event holds the whole workflow, and a
step_ended event a whole result. A run ID must name a file in the store's directory and nothing
beyond it (`path`); a run ID that can't name a file names no run. Runs reads the directory, which
sorts by name, and every run's file shares the `ext` suffix, so the IDs come back sorted.

Append reads the Seq of the last event first (`tail`), so it refuses to append after a corrupt
event. `tail` repairs a last line a crash left without its newline: one that decodes is kept, as
Events reads it, and gets its newline; one that doesn't decode is torn, and is cut. `lastLine`
reads backwards in chunks, so a long log isn't read whole.

## The event stream

Package [`workflow/sse`](../workflow/sse/).

An ended run with nothing after the cursor answers 204: an EventSource reconnects after any
stream that closes, and a status other than 200 is what tells it to stop. A run that hasn't ended
still streams, even when no runner is running it, so a client keeps reconnecting until a
restarted runner resumes it. The cursor is the Last-Event-ID header, which wins over the `after`
query, and zero when the request names none.

The stream sets `X-Accel-Buffering: no`, which tells nginx-style proxies to pass events through as
they come rather than buffer them. The keepalive ticker runs whether or not events flow: a comment
among busy events costs little, and no quiet stretch of the stream outlasts one period. `write`
names a logged event by its kind and gives its Seq as the id; a live exchange event carries none,
so the client's last id always names a logged event. The JSON is on one line, so the data needs
no splitting.

## The MCP bridge

Module [`mcpbridge`](../mcpbridge/).

**The respond tool.** The bridge keeps every name beginning `respond` (`reservedPrefix`) for
itself. `respondName` is RespondPrefix and the first 8 hex digits of the SHA-256 of the compacted
schema, so a schema that differs only in white space keeps the name. The tool's description asks
for exactly one call, because a response the bridge accepts ends the request's work, and says what
to do on rejection, because the model sees a rejected call only as a failed tool call. A tool that
declares no arguments gets `emptySchema`, because MCP requires every tool to have an object input
schema.

The Server's `schema`, `resolved`, and `respond` are respond's input schema, its compiled form,
and its name, all zero while no structured response is expected; `servers` are the go-sdk servers
of the open tunnels and endpoints. Each go-sdk server advertises the tools capability with
listChanged even with no tools yet, since respond may arrive later. `installRespondLocked` runs
with the Server's lock held.

`routeRespond` sends every call to a respond tool's name to `handleRespond`, whether or not the
server still lists that name. A call to a name the schema replaced or removed would otherwise fail
as a protocol error for an unknown tool, which a harness may not show the model; as a failed call,
it tells the model what to call instead. `handleRespond` accepts a call only to the current
respond tool, and validates it against the schema set when the call arrives: the schema may have
changed since the model listed its tools. `handleTool` reports any failure, of validation or of
the handler, as a tool result with isError set rather than a protocol error, so the model sees it
and the exchange carries on. A call that sent no arguments, or null, has an empty object.

**Waiting for a listing.** `routeRespond` also counts the tools/list requests for WaitListed.
`gen` counts the changes to respond. `listedGen` is the highest gen a tools/list answered, as each
stood when its request arrived, so a listing already under way when respond changed doesn't count
as the new one. `started` counts the listings that have arrived, answered or not, and `listed` is
closed and replaced at each answer. A harness's first listing may still be in flight when respond
changes, as OpenCode's is at session/new; the listing test drives `routeRespond` directly so it
can hold one in flight.

**The tunnel.** `tunnelConn` is the server's end of a tunnel. The server reads the messages
Deliver pushes; what it writes goes to the Deliver waiting on its id when it is a response, and to
outbound otherwise. A request id the harness reuses while its first request is still open is
refused, since the responses couldn't be told apart. When a Deliver's context ends, the tunnel
tells the server the request is cancelled, as the harness would, so the handler's context ends,
without waiting for the server to take the notification.

**The endpoint.** `Endpoint.Close` ends the sessions before it closes the HTTP server, because
their GET streams would otherwise hold their connections open. `requireToken` refuses with 401
every request that doesn't carry the token as a bearer token. `newToken` ignores
`crypto/rand.Read`'s error, because it never fails: the program crashes instead.

**The tests.** The raw tunnel client speaks JSON-RPC as a harness does, a message at a time, with
the server's own messages arriving on outbound, and its handshake runs at protocol version
2025-11-25, before the subscriptions of 2026-07-28, as a harness of that version does. The
go-sdk client's tunnel transport delivers each request on its own goroutine, because a request
may stay open, as subscriptions/listen does, until the tunnel closes.

## The Pi adapter

Module [`pi`](../pi/).

**Starting Pi.** `rpcArgs` start Pi in RPC mode without discovering the user's extensions,
skills, and context files, which would otherwise change the prompt or send dialogs no one answers.
Explicit -e and --skill paths still load, which is how the bridge and the session's skills arrive,
and Pi persists the session. `llamaProvider` is the name of Pi's llama.cpp provider, which Pi ships
as a built-in extension. Since Pi 0.99, -ne disables built-in extensions too, so for a session on
this provider the driver loads the extension by name; the user's extensions and the other
built-ins, such as MCP, stay out.

`writeAgentDir` writes the providers into the agent directory's models.json, creating the
directory private to the current user. Pi runs a "!" key as a command, and its settings can load
extensions, so no one else may write the directory: one that exists already, writable by others
or another user's, is refused. models.json is written to a temporary name and renamed, so a
session starting beside another that shares the directory never reads it half written.

`setModel` selects the session's model. On llama.cpp, a model Pi answers it doesn't list may still
arrive with Pi's background catalog refresh, so setModel asks again every `catalogPoll`, for up to
its wait. `catalogMiss` recognizes Pi's own answer that it doesn't list a llama.cpp model, the one
failure a catalog refresh can still mend; every other failure, such as Pi having exited or a model
another provider lacks, returns at once.

**The bridge.** `bridgeFiles` embeds the bridge extension, which the driver writes out and loads
into every Pi it starts: bridge.ts and the module it imports, respond.ts, and not the extension's
tests. It is the driver's required baseline: discovery stays off, so Pi runs this one extension
and none of the user's. Its requests are dialogs titled `callTitle` and `exchangeTitle`, and the
tool it registers for a structured response is `respondTool`; `toolsEnv` names the variable that
points it at the session's tool spec file, a list of `toolSpec`s. A tool that declares no
arguments gets `emptySchema`.

A `bridge` is what one session's Pi loads beyond itself: the bridge extension, the spec of the
session's tools, and its skills. The tool spec is the session's own, in a temporary directory that
Close removes. The extension and the skills are written once into the driver's cache, when it has
one, under names their content decides, so every session loads them from the same paths, and a
resumed session's history still names files that exist. Without a cache everything stays in the
session's temporary directory. `newBridge` refuses tool names that repeat or that are respond,
which the bridge keeps for structured responses. Pi reads skills from disk: a skill already there
is loaded where it lives, and any other is written into the cache under its name. Pi never
registers a tool its allowlist leaves out, so the allowlist names the session's tools and respond
as well as the harness's own.

`answerCall` runs the tool a `call` dialog names and renders the `answer`. A tool error, an
unknown tool, or a malformed call is the model's to see, as a failed tool result.
`answerExchange` renders the schema of the exchange whose run is starting, or none. `render`
renders an answer that won't marshal, such as a schema that isn't JSON, as an error instead, so
the bridge reports it rather than failing to parse an empty answer.

**The connection.** `connection` answers the bridge's dialogs: tool calls, and the schema of the
exchange whose run is starting. `schema` is the open exchange's, which the bridge asks for as each
run starts, and ending `run` ends the tool calls of the exchange Cancel cancels. `answer` handles
input dialogs, the bridge's: a tool call, answered once the tool has run, and the exchange's
schema. Any other dialog is dismissed, since no one is there to answer it, and a dialog left
waiting would hold up the run. `entryIDs` reads the entry IDs from a get_entries response's data.

**The codec.** `normalize` maps one Pi event to normalized events, without session or exchange IDs
or sequence numbers, which the session stamps. Every event maps to at least one normalized event,
so nothing Pi reports is lost: events with no normalized meaning become EventHarness. Pi's events
carry no request ID. An exchange is the window from agent_start to agent_settled, the one signal
that no retry, compaction, or queued message will start another run, so agent_settled maps to
EventEnded. `commandError` is Pi's answer that a command failed, as distinct from a transport
failure. `decodeUIRequest` turns an extension UI request into its event, and a dialog, which
waits on the driver's answer, into a Request as well.

Pi also ends system and user messages; only the assistant's carries a stop reason, so
`messageEnd` reads only an assistant's message_end. An aborted message is followed by
EventCancelled, and an errored one by EventError. `messageText` concatenates the text blocks of
an assistant message's content.

The respond tool is how the driver gets a structured response, not a tool of the session's, so
its call surfaces only as its outcome, `structured`. A successful call is an EventStructured: the
bridge returns the arguments the model called respond with, which Pi validated against the
exchange's schema, as the result's details. A failed call, whose arguments Pi rejected and
returned to the model, is an EventStructuredRejected with Pi's reason. A successful call whose
result carries no details is an EventError that is still the exchange's missing structured
response, harness.ErrNoStructuredResponse, so errors.Is matches it. `extensionError` is an
extension_error as an error, which names the extension and the event it failed in.

**The wire.** A `record` is one line of Pi's stdout: a response to a command when Type is
"response", whose ID echoes the command's id, and an event otherwise. message_update carries a
delta; message_start, message_end, and turn_end carry a message; the tool_execution_* events carry
the tool fields. A `message` is an AgentMessage: its content is a string for some roles and a
block list for assistant messages, so it stays raw until an assistant message needs its text.
`toolResult` is read only as far as the respond tool's details, the structured response, and a
failed call's content, which says why. A `uiRequest` has a line shape of its own: a confirm or
notify request's message is a string, where an event's is an AgentMessage. A `dialog` is an
extension_ui_request that waits for an answer, a select, confirm, input, or editor dialog, as
`dialogs` lists; the others, such as notify, are fire-and-forget. The bridge's requests are input
dialogs whose placeholder carries the request. A `dialogAnswer`'s Value answers an input dialog,
and Cancelled dismisses any dialog. A `command` is one line to Pi's stdin; fields a command
doesn't use stay empty. An `image` carries its data in base64, with no data: prefix; Pi replaces
it with a note for a model whose input doesn't include images, and still accepts the prompt.
`state` and `entries` read a get_state and a get_entries response's data, the latter as each
session entry's stable ID, in append order.

**The tests.** The test binary is its own fake Pi, selected by `fakeEnv`: "ok" answers prompts,
and "crash" dies during a run. `noRaceExitSleep` keeps a race-enabled fake from sleeping a second
at exit. A normal prompt replays the events of the captured plain.jsonl transcript. `longPrompt`
streams deltas until an abort arrives, then ends the way the captured aborted.jsonl transcript
does, including answering the abort only after agent_settled; the run is over by then, so the
fake accepts a prompt sent in response to it. The other prompts play the bridge's part: a
`toolPrompt` calls the tool named after its colon with {"q":"<rest>"} and ends the run with the
tool's result, or its error, as the text; `structuredPrompt` calls respond, when the exchange has
a schema, ending the run as the bridge's terminating tool does; `dialogPrompt` raises a confirm
dialog the driver must dismiss before the run goes on. The fake asks for the exchange's schema as
the prompt arrives, as the bridge does, and routes the driver's answers to its dialogs by id.

`launchEnv` names the file where the fake records its `launch`: its arguments, whether the -e
file exists (`ExtensionExists`), the tool spec the bridge would read, each --skill directory's
files by the directory (`SkillFiles`), and the models.json in PI_CODING_AGENT_DIR, when the
driver set one. A built-in extension, loaded by name, is no file of the driver's. `promptsEnv`
names the file where the fake appends each prompt command it receives, one JSON line each.

`fakeJournal` is the fake Pi's session: its entry `ids` and `kinds` in append order, shaped as the
capture in testdata/entries.jsonl shows Pi's. Setting the model on a new session appends three
entries (model_change, thinking_level_change, model_change), and on a resumed one appends one; a
session's first run appends a system message before the user message, and the assistant message
as it ends. IDs are unique within the session, across processes. With --session-dir the fake
persists the entries to <dir>/<session-id>.entries, one "id kind" line each, so a later fake
process resumes the session, and deleting the file loses it, as losing Pi's session file would.
`since` renders a get_entries response's data, or false when since names no entry. `lateModel`
is listed only `lateModelDelay` after the fake starts, as Pi's background catalog refresh adds a
model loaded since the catalog was saved; `absentModel` is never listed.

The resume tests run a second Pi process over a new Store on the same directory, as after a
restart, and check that each exchange is bound to the entries its run appended: the session's
system message, user, and assistant for the first, user and assistant for the second. The entries
that setting the model appended on each open belong to neither. The transcript and entries tests
replay what was captured from a real pi --mode rpc run against a llama.cpp router, and from Pi
0.87.1's get_entries responses. The setModel tests' wait is long, so a retry would show. A cache
or agent directory others can write to is refused, since Pi runs the bridge and the commands its
files name as code; a cache entry whose content no longer has its digest is written again, not
trusted.

## The Claude Code adapter

Module [`claude`](../claude/).

**Starting Claude Code.** `streamArgs` run Claude Code headless over stream-json in both
directions, with the partial messages that carry text as it streams. `isolationArgs` keep the
user's settings, CLAUDE.md files, hooks, and MCP servers out of the session, so the prompt and the
tools are the driver's: no setting sources load, and only the MCP servers --mcp-config names
connect. Permission prompts come to the driver as can_use_tool requests. `isolationEnv` keeps auto
memory out of the session and the CLI from updating itself under the driver; CLAUDECODE, set when
the driver runs inside a Claude Code session, would change how the harness behaves, so it is
cleared. CLAUDE_CONFIG_DIR, from ConfigDir, comes last in the environment, so the directory Claude
Code uses is the one `holds` looked in.

`sessionID` returns the session's ID and whether Claude Code already holds it. A new session's ID
is a new UUID, and an ID the caller names must be one, as Claude Code requires. `holds` looks for
a transcript of the session in any project: Claude Code stores a transcript under a directory
named after the session's working directory, and resumes the session from any directory. `lost`
fails with harness.ErrJournalMismatch when the caller names a session that Claude Code keeps no
transcript of but the store holds exchange records for: Claude Code lost the session, and opening
it would silently start a fresh one under the same ID. It stands in for the journal check a
harness.Journal gives, which Claude Code's stream doesn't offer.

`writePlugin` writes a plugin that carries the session's skills, named `pluginName`, so Claude
Code names each skill <plugin>:<skill>; with no skills there is no plugin. It stages the plugin in
a temporary directory, then caches it under a name its content decides, so a resumed session finds
the files its history names.

**The connection.** `connection` answers Claude Code's control requests: the MCP messages of the
driver's MCP server, which it relays through an mcpbridge tunnel, and permission checks. `allowed`
holds the harness tools the session allows; nil, for a session that keeps Claude Code's default
tools, allows every tool, and empty allows none but the driver's. `permit` lets the driver's own
tools and the allowed harness tools run, and denies anything else, since no one is there to ask.
`listWait` bounds how long Prompt waits for Claude Code to list the tools again after the respond
tool changed. Ending `run` ends the tool calls of the exchange Cancel cancels.

`relay` passes one MCP message to the MCP server and returns its response. A tool call
(`toolCall`) runs until Cancel ends the exchange's run, or until Claude Code exits. Any other
message, such as a tool listing, a ping, or the handshake, belongs to the session rather than the
run, so it runs until Claude Code exits: a listing Cancel cut short would leave Claude Code
without the tools. The reply to an MCP notification, which has no response of its own, is
`notificationAck`, as the Agent SDKs send. `outbound` relays a message the MCP server sends
unasked, such as notifications/tools/list_changed, to Claude Code as an mcp_message control
request; it runs on the server's goroutine, so it hands the message on and returns. `answer`
turns an error into an error reply.

`closeErr` is the error of Claude Code's exit once Close asked for it. When the last turn failed,
an interrupted one included, Claude Code exits with status 1 at the end of its input. The exchange
of that turn has already reported the failure, so closeErr ignores status 1. A harness killed
after WaitDelay, or any other status, is still an error.

**The codec.** Claude Code names the driver's MCP server's tools mcp__<server>__<tool>, so
`toolPrefix` starts the name of each of the driver's tools, and `toolName` strips it from a driver
tool's name and leaves a harness tool's as it is. Unlike Pi's codec, this one keeps state across
lines, which is safe because the Client has one reader goroutine: `respondArgs` keeps the
arguments of each respond call by its tool_use ID, which become the structured response once
Claude Code reports the call's result; `toolNames` maps the ID of every other call to its tool's
name, which Claude Code's tool results don't carry; and `limit` keeps the turn's last rate-limit
notice, which explains a turn the limit refused.

`normalize` maps every stream-json line to at least one normalized event, so nothing Claude Code
reports is lost: lines with no normalized meaning become EventHarness. A turn starts at the system
init message, which Claude Code sends as each turn begins, and ends at its result, which maps to
EventMessageEnd and EventEnded. The init forgets the last turn's rate-limit notice, so a notice
from an earlier turn doesn't explain this turn's failure. Claude Code accepts a user message
during a turn and runs it as a later turn, but harness.Session never sends one. Text and thinking
arrive as stream deltas; the assistant messages that follow repeat them whole, so only their tool
calls are read. A model whose thinking Claude Code doesn't display streams empty thinking deltas.

`toolCalls` maps an assistant message's tool_use blocks to EventToolCall, but keeps a respond call
for its result instead: it is how the driver gets a structured response, not a tool of the
session's. `toolResults` maps a user message's tool_result blocks to EventToolResult. A respond
call's result becomes the structured response when mcpbridge accepted its arguments, which it
validated against the exchange's schema, and an EventStructuredRejected with the rejection the
model saw otherwise. A result is the bridge's acceptance when it isn't an error and its text
(`resultText`, from a string or the text blocks of a list) is mcpbridge.Accepted, so the bridge's
verdict decides, not Claude Code's error flag alone. `blocksOf` decodes a message's content
blocks; a user message's content may be a plain string, which holds no blocks.

`end` maps a turn's result to EventMessageEnd, with the turn's text and its usage summed over its
model requests, and EventEnded. Claude Code reports an interrupted turn as an error whose terminal
reason starts with "aborted", which maps to the stop reason "aborted" and EventCancelled; any
other error is EventError. `turnError` makes a turn the subscription's limit refused, by a
rejected rate-limit notice, an HTTP 429, or a usage-limit message, a *harness.LimitError, reset
when the notice or the message says; `limitReset` finds the reset time in the usage-limit
message, which ends with "|<unix seconds>". `limitStatus` is a rate_limit_event's notice, with the
limiting window's utilization.

**The wire.** Every stream-json line is one JSON object whose type field says what it is, and
every stdout line shares the head `line`. The driver writes user messages and control requests,
and reads everything else; the wire types name only what it reads and writes. A `userMessage` is
a user turn the driver sends; Claude Code answers it with no response of its own, and the turn's
outcome arrives as events, ending with a result. A `contentBlock` is text, an image (Source), a
tool call (ID, Name, and Input), or a tool's result (ToolUseID, Content, and IsError, its Content
a string or a list of blocks). A `controlRequest` goes in either direction, the driver's such as
initialize or interrupt, Claude Code's such as mcp_message or can_use_tool, and names only the
fields of the subtypes the driver uses: initialize's Hooks, where null registers none;
mcp_message's ServerName and Message, one JSON-RPC message for the named SDK MCP server; and
can_use_tool's ToolName and Input. `controlEnvelope` carries a control request on the wire, and
`controlResponse` answers one, in either direction, with a reply subtype of success or error. A
`streamEvent` wraps one raw Messages API stream event, sent with --include-partial-messages. A
`message` is an assistant's tool calls or the tool results Claude Code sends back as the user. A
`result` ends a turn, with its `usage` summed over the turn's model requests, and `rateLimit` is
where the subscription stands against its limits.

**The tests.** The test binary is its own fake Claude Code, selected by `fakeEnv`, which speaks
enough stream-json to drive the connection: the control protocol in both directions, the driver's
tool server through mcp_message requests as Claude Code makes them, numbered from 1 as Claude
Code's are, with the MCP handshake made when the first message arrives, and turns that end with a
result. `noRaceExitSleep` keeps a race-enabled fake from sleeping a second at exit. `launchEnv`
and `promptsEnv` name files where the fake records its arguments and the user messages it
receives, one JSON line each, and `configEnv` a file where it records its $CLAUDE_CONFIG_DIR. The
fake's `waiting` maps each of its control requests' IDs to its reply; `tools` is the driver's tool
list as it last listed it; `listing` counts the listings under way, which a turn waits for, as
Claude Code applies a changed tool list before it starts the next turn; `userBlocks` carries each
user message's content blocks; and `failed` makes it exit with status 1 at the end of its input
after a failed turn, as Claude Code does. When the driver's server announces a change, the fake
lists the tools again.

Each user message's text, up to the respond instruction the driver appends after a blank line,
decides what the fake's turn does: "tool:<name>:<args>" calls the driver's tool with the
arguments, reporting the call and its result as Claude Code does; "structured:<args>" calls the
respond tool the fake last listed; "permit:<tool>" asks the driver whether the tool may run and
answers with the decision; "stream" streams text until interrupted, ending with a terminal reason
"aborted_*"; anything else answers "hello". Since the fake answers with whatever respond it
listed last, the schema-change test passes only if the prompt waited for the new listing.

## The OpenCode adapter

Module [`opencode`](../opencode/).

**Starting OpenCode.** `isolationEnv` keeps the user's Claude Code files and outside skills out of
the session, and keeps OpenCode from fetching its model catalog or updating itself under the
driver. `handshake` initializes ACP, creates or loads the session with the driver's MCP server,
and selects the model. `config` renders OpenCode's configuration for a session: the session's
provider alone, with its model listed; the session's skills as skill paths; and, when
opts.HarnessTools is set, a tool allowlist of those tools and the driver's. A state directory
others can write to is refused before OpenCode starts, since OpenCode reads configuration from
it that can name commands to run; one that doesn't exist yet is created private to the user.

**The connection.** `connection` serves one ACP session. It answers OpenCode's permission
requests; the session's tools run in the driver's MCP server, which OpenCode reaches over HTTP.
`listWait` bounds how long Prompt waits for OpenCode to list the tools again after the respond
tool changed: OpenCode lists them when the server says they changed, in its own time, and a
prompt that overtakes the listing reaches a model that doesn't see the new respond tool. Nothing
waits on the prompt's response channel: the answer, or its failure, reaches the exchange through
the codec's events, and the harness's exit through the session. The channel holds its one
response unread, and goes with the call.

`answer` answers a permission request with "once", which allows the call, and anything else, such
as fs/write_text_file after an edit, with an error, since the driver offers no client
capabilities. The tool allowlist in OpenCode's configuration is the gate: with opts.HarnessTools
set, it denies every tool outside them and the driver's own before OpenCode asks, so a tool it
asks about is one the session enables. `answer` can't gate on the request either: OpenCode
1.18.34 (packages/opencode/src/acp/permission.ts) titles it permissionTitle(toolName, input), a
description of the call built from its input, rather than the tool's stable name.

**The codec.** OpenCode names the driver's MCP server's tools <server>_<tool>, so `toolPrefix`
starts the name of each of the driver's tools, and `toolName` strips it from a driver tool's name
and leaves a harness tool's as it is. ACP's updates carry no request ID, but OpenCode sends every
update of a turn before it answers the turn's session/prompt, so the codec ends the turn when it
decodes that answer, in stream order. It keeps state across lines: `prompts` and `loads`, the
ids of the session/prompt and session/load calls in flight; `started`, whether the turn has
reported EventStarted; `messageID` and `text`, the turn's last agent message and its text so far;
and `toolNames` and `toolArgs`, each tool call's tool and arguments by its ID, with `reported`
holding the calls already reported. Encode runs on callers' goroutines and Decode on the reader's,
so the state is locked; `update`, `tool`, and `end` run with the lock held.

`notification` maps session/update's notifications to events, and anything else to EventHarness.
Updates that arrive while a session/load is in flight replay the session's history, and belong to
no exchange, so they map to EventHarness too. `errorData` renders a JSON-RPC error's data, which
OpenCode uses to name the failing service. `chunkText` is a message chunk's text, when its content
is a text block, and `outputText` a tool's output as text: a string as it is, or the JSON.

`tool` maps a tool call's updates. The call surfaces as EventToolCall once OpenCode runs it, with
its arguments, or once it ends, whichever is first: a call is pending with no arguments before it
runs, and a tool that takes none never has any, its rawInput staying {}. It surfaces as
EventToolResult once it completes or fails. A respond call surfaces only as the structured
response, or its rejection. It is accepted when it completed with the bridge's acceptance as its
output. The status alone usually decides: OpenCode 1.18.34 (packages/opencode/src/mcp/catalog.ts)
throws on an MCP result with isError set, which the bridge's rejections carry, so the call fails.
The bridge's verdict is checked as well, so the outcome doesn't rest on how another program maps
an MCP result to a status.

`end` maps a session/prompt's answer to the turn's end: EventMessageEnd with the turn's last
message and its usage, then EventEnded. OpenCode answers a cancelled turn with stop reason
"cancelled", which maps to "aborted" and EventCancelled, and a failed one with a JSON-RPC error,
which maps to EventError.

**The wire.** ACP as OpenCode speaks it is JSON-RPC 2.0, one message per line, at protocol
version 1; the wire types name only the fields the driver reads or writes. A `call` is a request
or notification the driver sends, to which Encode adds the id, or none for a notification. An
`rpcMessage` is any JSON-RPC message, read far enough to tell a response from a request or a
notification, and a `request` is one OpenCode makes of the driver, such as
session/request_permission. A `contentBlock` is one block of a prompt or of an update's content.
An `mcpServer` names the driver's own MCP server, over HTTP, for a session. A `sessionUpdate` is a
session/update notification's update: its Content is a message chunk's content block or a tool
call's list of content, and ToolCallID, Title, Kind, Status, RawInput, and RawOutput are a tool
call's. A `promptResult` is session/prompt's result, which arrives as the turn ends.

**The tests.** The test binary is its own fake OpenCode, selected by `fakeEnv`, which speaks
enough ACP to drive the connection: the handshake, sessions it persists under XDG_DATA_HOME,
turns that end with session/prompt's answer, cancellation, permission requests, and the driver's
tools, which it calls over HTTP with go-sdk's MCP client, adding the server's Authorization header
to every request, as OpenCode calls them at session/new. `noRaceExitSleep` keeps a race-enabled
fake from sleeping a second at exit. `launchEnv` names a file where the fake records its arguments
and configuration, and `sessions` names the file where it keeps the IDs of the sessions it created; a
loaded session's history replays before the answer, and belongs to no exchange. The fake's
`listing` counts listings under way, which a turn waits for, as OpenCode applies a changed tool
list before it runs the next prompt. `cancelled` is closed by session/cancel to cancel the turn
under way, and is nil while none is. A turn is under way from the moment its session/prompt is
read, so a cancel read after it cancels it, and a cancel read while the session is idle is
ignored, as OpenCode ignores it.

A prompt's text decides what the fake's turn does: "tool:<name>:<args>" calls the driver's tool
with the arguments, reporting the call as OpenCode does, pending with no arguments, in progress
with them, then completed or failed; "structured:<args>" calls the respond tool the fake last
listed; "permit:<title>" asks the driver whether a call titled so may run, and answers with the
option; "image" answers with the media types of the prompt's images; "stream" streams text until
cancelled; anything else answers "hello". Since the fake answers with the respond tool it listed
last, the schema-change test passes only if the prompt waited for the listing. The transcript
replay holds OpenCode's side only, so it encodes a session/prompt under each id that answers with
a stop reason, before the turn it ends; the handshake ends with the model's selection, answered
as id 3. A cancel the session sends as soon as Prompt returns reaches OpenCode after the prompt,
so it aborts the turn: an exchange whose context has already ended is aborted at once.
