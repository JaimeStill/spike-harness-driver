// Package opencode is the harness.Connection adapter for OpenCode, driven over the Agent
// Client Protocol (opencode acp): JSON-RPC 2.0 on stdio, at protocol version 1. One OpenCode
// process runs one session, through a stdio.Client.
//
// # The protocol
//
// The driver initializes, creates or loads a session with session/new or session/load, and
// selects the model with session/set_config_option. A turn is one session/prompt: OpenCode
// streams session/update notifications, which carry no request ID, and answers the prompt only
// as the turn ends, with its stop reason and usage, after every update of the turn. So Prompt
// returns once the request is on its way, and the codec ends the exchange when it decodes the
// answer, in stream order. session/cancel is a notification, and the cancelled turn's answer
// has stop reason "cancelled", which maps to "aborted". A failed turn is a JSON-RPC error.
//
// # Tools, structured responses, and skills
//
// OpenCode reaches tools a program defines only as an MCP server's. ACP's session/new names the
// session's MCP servers, so the session's tools are an mcpbridge server on a loopback port,
// named with its URL and an Authorization header carrying its token. OpenCode names the
// server's tools driver_<tool>. A request's schema becomes the bridge's respond tool; OpenCode
// lists the tools when the session opens and again when the server says they changed, so
// Prompt waits for that listing before it sends a prompt whose respond tool is new. ACP has no
// response schema of its own.
//
// Skills are OpenCode's skill paths: a skill on disk where it lives, any other written into the
// driver's cache. The model loads one with OpenCode's skill tool, when HarnessTools allows it.
//
// # Isolation, models, and sessions
//
// OpenCode reads its configuration, data, state, and cache from the XDG directories under the
// driver's StateDir, so the user's own stay out; --pure keeps the user's plugins out; and the
// environment keeps out the user's Claude Code files and outside skills, and stops OpenCode
// fetching its model catalog or updating itself. The configuration, in
// OPENCODE_CONFIG_CONTENT, enables only the session's provider, with the session's model
// listed, since OpenCode selects only a model its configuration or catalog lists; a listed
// model is selectable at once. When HarnessTools is set, the configuration allows those tools
// and the driver's, and OpenCode denies the rest without asking.
//
// OpenCode keeps sessions in its database under StateDir and loads one by ID from any working
// directory, replaying its history as updates, which belong to no exchange. It can't create a
// session under an ID the caller chooses, and fails loudly on an ID it doesn't hold. The stream
// offers no read of a session's entries, so the connection keeps no harness.Journal.
package opencode
