// Package mcpbridge gives a session's Go tools to a harness through the Model Context
// Protocol. It is for a harness with no in-band route for the driver's tools: Pi loads an
// extension that calls back over its RPC channel, but Claude Code and OpenCode reach tools the
// program defines only as an MCP server's. The bridge is that server, running in the driver's
// process, so a tool call still runs the harness.Tool's handler here.
//
// A Server is reached two ways, and an adapter uses the one its harness speaks:
//
//   - Tunnel connects the server over an in-process transport that carries one JSON-RPC
//     message at a time. Claude Code's "sdk" MCP servers work this way: the CLI wraps each
//     message in a control request, and the adapter passes it to Tunnel.Deliver and wraps the
//     reply in its control response.
//   - Listen serves streamable HTTP on a loopback port. OpenCode's ACP session/new names an
//     http MCP server by URL and headers, so the adapter passes it the Endpoint's URL and an
//     Authorization header carrying its token.
//
// # Structured responses
//
// A request with a schema asks the model for a structured response. SetSchema offers it a
// respond tool, whose input schema is the request's schema, and the model answers by calling
// it with the response as its arguments. The tool's name, RespondPrefix and a digest of the
// schema, changes with the schema, because Claude Code caches a tool's schema by its name; a
// call to a name the schema replaced or removed fails, and tells the model which to call.
//
// The bridge validates a response itself, in Go, against the schema set when the call arrives, so
// it doesn't rely on the harness to. A valid call's result is Accepted; an invalid one's is the
// validation error, as a failed call, so the model can correct the response and call respond
// again. An adapter reads the verdict from the harness's report of the call's result, and may also
// take it from the optional Options.OnStructured and Options.OnRejected. The bridge validates every tool's arguments the
// same way, which keeps the promise harness.ToolHandler makes that its arguments match the tool's
// schema.
//
// Adding, replacing, or removing respond changes the server's tool list, and go-sdk tells
// each connected session with notifications/tools/list_changed. A session that negotiated
// protocol version 2026-07-28 or later receives it only after it subscribes with
// subscriptions/listen; a session on an earlier version receives it unasked.
//
// # The HTTP token
//
// Any local user can connect to a loopback port, and the tools run with the driver's
// privileges, so the HTTP endpoint answers only a request that carries the endpoint's token:
// 32 random bytes, hex-encoded, as "Authorization: Bearer <token>". Every other request is
// refused with 401 Unauthorized before it reaches the MCP server. The comparison runs in
// constant time, so the response's timing gives no clue to the token.
package mcpbridge
