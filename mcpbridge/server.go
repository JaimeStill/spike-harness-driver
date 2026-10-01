package mcpbridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/JaimeStill/spike-harness-driver/harness"
)

// RespondPrefix begins the name of the tool the model calls with a structured response. The
// rest of the name is drawn from the response schema, so the name changes with the schema:
// Claude Code caches a tool's input schema by its name, so a new schema under the old name
// would keep reaching the model as the old one. The bridge keeps every name beginning
// "respond" for itself.
const RespondPrefix = "respond_"

// reserved begins every name the bridge keeps for itself, so no session tool may begin with it.
const reserved = "respond"

// IsRespond reports whether name is a respond tool's, current or not, so an adapter can tell
// the model's structured response from its other tool calls.
func IsRespond(name string) bool { return strings.HasPrefix(name, RespondPrefix) }

// DefaultName is the MCP server's name when Options.Name is empty.
const DefaultName = "driver"

// respondName is the respond tool's name for schema: RespondPrefix and the first 8 hex digits
// of the SHA-256 of the compacted schema, so a schema that differs only in white space keeps
// the name. schema is valid JSON.
func respondName(schema json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Compact(&b, schema); err != nil {
		b.Reset()
		b.Write(schema)
	}
	sum := sha256.Sum256(b.Bytes())
	return RespondPrefix + hex.EncodeToString(sum[:4])
}

// respondDescription tells the model how to use respond. It asks for exactly one call because
// a response the bridge accepts ends the request's work, and it says what to do on rejection
// because the model sees a rejected call only as a failed tool call.
const respondDescription = "Give your final answer by calling this tool exactly once, with the answer as its " +
	"arguments. The arguments must match this tool's input schema. If the call is rejected, " +
	"correct the arguments as the error says and call it again."

// emptySchema is the arguments schema of a tool that declares none. MCP requires every tool
// to have an object input schema.
var emptySchema = json.RawMessage(`{"type":"object","properties":{}}`)

// Options configures a Server.
type Options struct {
	// Name is the MCP server's name, which the harness shows the model as the tools' prefix.
	// Empty means DefaultName.
	Name string
	// OnStructured receives the arguments of each respond call that matches the current
	// schema. It runs on the call's goroutine, before the model learns the response was
	// accepted. Nil drops them.
	OnStructured func(value json.RawMessage)
	// OnRejected receives the validation error of each respond call that doesn't match the
	// current schema, or that names a respond tool other than the current one, as after the
	// schema changed or was removed. Nil drops it.
	OnRejected func(err error)
}

// Server is the MCP server that offers a session's tools to its harness. One Server may be
// reached through any number of tunnels and endpoints at once; each gets its own go-sdk
// server, so closing one closes only its own sessions, and SetSchema updates them all.
type Server struct {
	name  string
	opts  Options
	tools []tool

	mu sync.Mutex
	// schema is respond's input schema, resolved its compiled form, and respond its name;
	// all are zero while no structured response is expected.
	schema   json.RawMessage
	resolved *jsonschema.Resolved
	respond  string
	// servers are the go-sdk servers of the open tunnels and endpoints.
	servers map[*mcp.Server]struct{}

	// gen counts the changes to respond. listedGen is the gen of the last tools/list answered,
	// as it stood when the request arrived, so a listing already under way when respond
	// changed doesn't count as the new one; listings counts the answered listings, and listed
	// is closed and replaced at each, for WaitListed.
	gen, listedGen, listings int
	listed                   chan struct{}
}

// tool is a session tool with its schema compiled for validating its arguments.
type tool struct {
	harness.Tool
	resolved *jsonschema.Resolved
}

// New returns a server offering tools. Each tool must be valid, its name unique and not
// begin with "respond", and its schema one jsonschema-go can compile, since the bridge validates each
// call's arguments against it.
func New(tools []harness.Tool, opts Options) (*Server, error) {
	s := &Server{name: opts.Name, opts: opts, servers: map[*mcp.Server]struct{}{}, listed: make(chan struct{})}
	if s.name == "" {
		s.name = DefaultName
	}
	seen := map[string]bool{}
	for _, t := range tools {
		if err := t.Validate(); err != nil {
			return nil, fmt.Errorf("mcpbridge: %w", err)
		}
		if strings.HasPrefix(t.Name, reserved) {
			return nil, fmt.Errorf("mcpbridge: tool %q: names beginning %q are the bridge's own", t.Name, reserved)
		}
		if seen[t.Name] {
			return nil, fmt.Errorf("mcpbridge: two tools are named %q", t.Name)
		}
		seen[t.Name] = true
		if len(t.Schema) == 0 {
			t.Schema = emptySchema
		}
		resolved, err := compile(t.Schema)
		if err != nil {
			return nil, fmt.Errorf("mcpbridge: tool %q: %w", t.Name, err)
		}
		s.tools = append(s.tools, tool{Tool: t, resolved: resolved})
	}
	return s, nil
}

// SetSchema sets the schema of the structured response the model must give. A schema installs
// a respond tool with the schema as its input schema, under the name RespondName then
// returns; a different schema replaces it with one of another name. Nil or empty removes
// respond. A schema that isn't an object schema jsonschema-go can compile is rejected, and
// respond is left as it was.
func (s *Server) SetSchema(schema json.RawMessage) error {
	var (
		resolved *jsonschema.Resolved
		name     string
	)
	if len(bytes.TrimSpace(schema)) > 0 {
		var err error
		if resolved, err = compile(schema); err != nil {
			return fmt.Errorf("mcpbridge: response schema: %w", err)
		}
		name = respondName(schema)
	} else {
		schema = nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if name == s.respond {
		return nil
	}
	old := s.respond
	s.schema, s.resolved, s.respond = schema, resolved, name
	s.gen++
	for srv := range s.servers {
		if old != "" {
			srv.RemoveTools(old)
		}
		s.installRespond(srv)
	}
	return nil
}

// WaitListed waits until a harness has listed the tools since respond last changed, for up to
// max, so a prompt sent next finds the current respond tool. A harness learns of the change by
// notifications/tools/list_changed and lists the tools again in its own time; a prompt that
// overtakes the listing reaches a model that sees the old tool. WaitListed returns at once when
// no harness has listed the tools yet, since its first listing will be current, and returns nil
// when max passes, since a call to a stale respond tool still tells the model the current name.
func (s *Server) WaitListed(ctx context.Context, max time.Duration) error {
	deadline := time.After(max)
	for {
		s.mu.Lock()
		done, listed := s.listings == 0 || s.listedGen >= s.gen, s.listed
		s.mu.Unlock()
		if done {
			return nil
		}
		select {
		case <-listed:
		case <-deadline:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// RespondName returns the current respond tool's name, which an adapter may give the model in
// its instructions, or "" while no schema is set.
func (s *Server) RespondName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.respond
}

// newServer returns a go-sdk server offering the session's tools and, when a schema is set,
// respond, and registers it for SetSchema. remove unregisters it.
func (s *Server) newServer() (srv *mcp.Server, remove func()) {
	srv = mcp.NewServer(&mcp.Implementation{Name: s.name, Version: "1"}, &mcp.ServerOptions{
		// Advertise the tools capability with listChanged even with no tools yet, since
		// respond may arrive later.
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}},
	})
	srv.AddReceivingMiddleware(s.routeRespond)
	for _, t := range s.tools {
		srv.AddTool(&mcp.Tool{Name: t.Name, Description: t.Description, InputSchema: t.Schema}, s.handleTool(t))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installRespond(srv)
	s.servers[srv] = struct{}{}
	return srv, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.servers, srv)
	}
}

// installRespond adds srv's respond tool for the current schema, if one is set. The caller
// holds s.mu.
func (s *Server) installRespond(srv *mcp.Server) {
	if s.respond == "" {
		return
	}
	srv.AddTool(&mcp.Tool{Name: s.respond, Description: respondDescription, InputSchema: s.schema}, s.handleRespond)
}

// routeRespond sends every call to a respond tool's name to handleRespond, whether or not the
// server still lists that name, and counts the tools/list requests answered, for WaitListed. A call to a name the schema replaced or removed would
// otherwise fail as a protocol error, for an unknown tool, which a harness may not show the
// model; as a failed call, it tells the model what to do instead.
func (s *Server) routeRespond(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if call, ok := req.(*mcp.CallToolRequest); ok && call.Params != nil && IsRespond(call.Params.Name) {
			return s.handleRespond(ctx, call)
		}
		if method != "tools/list" {
			return next(ctx, method, req)
		}
		s.mu.Lock()
		gen := s.gen
		s.mu.Unlock()
		res, err := next(ctx, method, req)
		if err == nil {
			s.mu.Lock()
			s.listings++
			s.listedGen = max(s.listedGen, gen)
			close(s.listed)
			s.listed = make(chan struct{})
			s.mu.Unlock()
		}
		return res, err
	}
}

// handleTool runs t for each call. A failure, whether of validation or of the handler, is a
// tool result with isError set rather than a protocol error, so the model sees it and the
// exchange carries on.
func (s *Server) handleTool(t tool) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := arguments(req)
		if err := validate(t.resolved, args); err != nil {
			return failure(fmt.Errorf("the arguments don't match the tool's schema: %w", err)), nil
		}
		text, err := t.Handler(ctx, args)
		if err != nil {
			if ctx.Err() != nil {
				err = errors.Join(err, context.Cause(ctx))
			}
			return failure(err), nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
	}
}

// handleRespond takes a structured response. It accepts a call only to the current respond
// tool, and validates it against the schema set when the call arrives: the schema may have
// changed since the model listed its tools.
func (s *Server) handleRespond(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	s.mu.Lock()
	resolved, current := s.resolved, s.respond
	s.mu.Unlock()

	args := arguments(req)
	var err error
	switch {
	case current == "":
		err = fmt.Errorf("%s: no structured response is expected now", req.Params.Name)
	case req.Params.Name != current:
		err = fmt.Errorf("%s: the response schema changed; call %s instead", req.Params.Name, current)
	default:
		if err = validate(resolved, args); err != nil {
			err = fmt.Errorf("the response doesn't match the schema: %w", err)
		}
	}
	if err != nil {
		if s.opts.OnRejected != nil {
			s.opts.OnRejected(err)
		}
		return failure(err), nil
	}
	if s.opts.OnStructured != nil {
		s.opts.OnStructured(args)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Accepted."}}}, nil
}

// arguments returns a call's arguments, an empty object for a call that sent none or null.
func arguments(req *mcp.CallToolRequest) json.RawMessage {
	if req.Params == nil {
		return json.RawMessage(`{}`)
	}
	if a := bytes.TrimSpace(req.Params.Arguments); len(a) == 0 || bytes.Equal(a, []byte("null")) {
		return json.RawMessage(`{}`)
	}
	return req.Params.Arguments
}

// compile checks that schema is an object schema and compiles it for validation.
func compile(schema json.RawMessage) (*jsonschema.Resolved, error) {
	if err := harness.ValidateSchema(schema); err != nil {
		return nil, err
	}
	var js jsonschema.Schema
	if err := json.Unmarshal(schema, &js); err != nil {
		return nil, fmt.Errorf("%w: %w", harness.ErrInvalidSchema, err)
	}
	resolved, err := js.Resolve(nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", harness.ErrInvalidSchema, err)
	}
	return resolved, nil
}

// validate checks args against resolved.
func validate(resolved *jsonschema.Resolved, args json.RawMessage) error {
	var v any
	if err := json.Unmarshal(args, &v); err != nil {
		return fmt.Errorf("the arguments aren't JSON: %w", err)
	}
	return resolved.Validate(v)
}

// failure is a tool result reporting err to the model.
func failure(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
}
