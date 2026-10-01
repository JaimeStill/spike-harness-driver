package mcpbridge

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Endpoint is the server served over streamable HTTP on a loopback port.
//
// The endpoint is stateful: each client that initializes gets a session, named by its
// Mcp-Session-Id header, and may hold a GET stream open on which the server sends what it
// initiates. That stream is how notifications/tools/list_changed reaches the harness when
// SetSchema changes the tool list; a stateless endpoint has no such stream and keeps no
// session to notify.
type Endpoint struct {
	// URL is the endpoint's address, such as "http://127.0.0.1:41234/mcp".
	URL string
	// Token is the bearer token every request must carry, as "Authorization: Bearer <token>".
	Token string

	srv    *mcp.Server
	remove func()
	http   *http.Server
	stop   func() bool
	once   sync.Once
	err    error
}

// Listen serves the server over streamable HTTP on 127.0.0.1, on a port the system picks,
// under a new random token. The endpoint lasts until Close or until ctx ends.
func (s *Server) Listen(ctx context.Context) (*Endpoint, error) {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("mcpbridge: listen: %w", err)
	}
	token := newToken()
	srv, remove := s.newServer()
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	e := &Endpoint{
		URL:    "http://" + ln.Addr().String() + "/mcp",
		Token:  token,
		srv:    srv,
		remove: remove,
		http: &http.Server{
			Handler:           requireToken(token, handler),
			ReadHeaderTimeout: 10 * time.Second,
		},
	}
	go func() { _ = e.http.Serve(ln) }()
	e.stop = context.AfterFunc(ctx, func() { _ = e.Close() })
	return e, nil
}

// Close stops serving, ending every session the endpoint carries and closing the
// connections they held open.
func (e *Endpoint) Close() error {
	e.once.Do(func() {
		e.stop()
		// Ending the sessions first closes their GET streams, which would otherwise hold
		// their connections open.
		var errs []error
		for ss := range e.srv.Sessions() {
			errs = append(errs, ss.Close())
		}
		errs = append(errs, e.http.Close())
		e.remove()
		e.err = errors.Join(errs...)
	})
	return e.err
}

// requireToken passes on only the requests that carry token as a bearer token, and refuses
// the rest with 401 Unauthorized.
func requireToken(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// newToken returns 32 random bytes, hex-encoded. crypto/rand.Read never fails: the program
// crashes instead.
func newToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
