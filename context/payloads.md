# Payloads

Planned for step 3, alongside tool calls and skills.

- `Event.Err` becomes an `error` rather than a string, so callers can match an error that
  arrives as an event with `errors.Is`. The cost is that events no longer serialize directly.
- The `Connection` reports its exit error explicitly. Today the session infers the error from
  the last event, so a run that ended in error before Close lends its error to the session.
- `Usage` holds input and output tokens only. Pi also reports cache reads and writes, and with a
  warm cache the input count alone misleads (1 token in the step 1 runs).

Rejected: tau's `response.StreamingResponse` and `ContentBlock` (`tau-protocol`) as the event
types. They are shaped per HTTP call, carry no session or exchange IDs or lifecycle events, and
`ContentBlock` is sealed through an unexported method, so no other package can add a kind.
