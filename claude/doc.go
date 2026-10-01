// Package claude is the harness.Connection adapter for Claude Code, driven headless over
// stream-json in both directions (claude -p --input-format stream-json --output-format
// stream-json). One Claude Code process runs one session, through a stdio.Client.
//
// # The protocol
//
// The driver writes user messages, which Claude Code doesn't answer, and control requests,
// which it answers by request_id: initialize at start, and interrupt to cancel. Claude Code
// writes the turn's events and control requests of its own, which the driver answers: the MCP
// messages of the driver's tool server, and permission checks for tools. A turn starts at the
// system init message and ends at its result, which carries the turn's text and its usage
// summed over the turn's model requests; an interrupted turn ends with a result whose terminal
// reason starts with "aborted".
//
// # Tools, structured responses, and skills
//
// Claude Code reaches tools a program defines only as an MCP server's, so the session's tools
// are an mcpbridge server that Claude Code connects to as an "sdk" server, relayed through
// mcp_message control requests. A request's schema becomes the bridge's respond tool, whose
// name changes with the schema because Claude Code caches a tool's schema by its name. The
// connection sends the tool-list change and waits for Claude Code to list the tools again
// before it sends the message. Claude Code's own --json-schema isn't used: it is fixed for
// the whole process, and a session's exchanges each carry their own schema.
//
// Skills are written into a plugin in the driver's cache, which Claude Code loads with
// --plugin-dir and names <plugin>:<skill>. The model invokes one through the Skill tool, when
// HarnessTools allows it, and a prompt of /driver:<skill> inlines it with no tool at all.
//
// # Isolation and sessions
//
// No setting sources load, so the user's settings, CLAUDE.md files, hooks, and MCP servers stay
// out; only the driver's MCP server connects; auto memory and self-updating are off. Claude
// Code's bundled skills still load. --bare would isolate more, but it refuses the subscription
// login the driver runs on.
//
// Claude Code persists each session and resumes it by ID from any working directory. The driver
// resumes an ID Claude Code keeps a transcript of, under its configuration directory, and
// creates a session under any other; Claude Code itself fails loudly when asked to resume an
// ID it doesn't hold. The connection keeps no harness.Journal: the stream offers no read of the
// session's entries.
//
// Usage limits: Claude Code reports where the subscription stands with rate_limit_event, which
// becomes harness.EventLimit, and a turn the limit refused becomes a *harness.LimitError.
package claude
