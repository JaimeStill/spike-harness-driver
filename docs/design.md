# Design

The reasoning behind the root module's packages and the MCP bridge: what each unexported part
does and why it is built that way. Each package's own documentation, which `go doc` prints, gives
its API and points to its section here.

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
