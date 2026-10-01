package workflow

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/JaimeStill/spike-harness-driver/clutch/output"
	"github.com/JaimeStill/spike-harness-driver/workflow/sse"
)

// readHeaderTimeout bounds reading a request's headers. No other server timeout applies, since
// an event stream lasts as long as its run.
const readHeaderTimeout = 10 * time.Second

// ServeCommand builds "serve", which serves the runs over HTTP until it is signalled.
func ServeCommand(svc *Service, out *output.Output) *cobra.Command {
	var (
		addr      string
		limit     int
		keepalive time.Duration
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve workflow runs over HTTP, with their events as Server-Sent Events",
		Long: "serve runs workflows for HTTP clients:\n\n" +
			"  POST   /runs              start a run of the workflow in the body; 201 with its id\n" +
			"  GET    /runs              every run's state\n" +
			"  GET    /runs/{id}         one run's state\n" +
			"  GET    /runs/{id}/events  the run's events as Server-Sent Events; a reconnect with\n" +
			"                            Last-Event-ID (or ?after=N) picks up after that event\n" +
			"  DELETE /runs/{id}         cancel the run\n\n" +
			"At start, it resumes every run the log under --state leaves unfinished, as after a\n" +
			"crash or a restart. On SIGINT or SIGTERM it stops its runs without ending them, so\n" +
			"the next serve resumes them, and closes its event streams. --limit bounds the\n" +
			"exchanges in flight across every run.\n\n" +
			"The server has no authentication: it listens on loopback unless --addr says otherwise.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGTERM)
			defer stop()
			return serve(ctx, svc, out, addr, limit, sse.Options{Keepalive: keepalive})
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8080", "the address to listen on")
	cmd.Flags().IntVar(&limit, "limit", 4, "the most exchanges in flight at once across every run (0 is no limit)")
	cmd.Flags().DurationVar(&keepalive, "keepalive", sse.DefaultKeepalive, "how often an idle event stream gets a keepalive comment")
	return cmd
}

func serve(ctx context.Context, svc *Service, out *output.Output, addr string, limit int, opts sse.Options) error {
	r, err := svc.Runner(limit)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	resumed, rerr := r.ResumeAll(ctx)
	for _, id := range resumed {
		out.Printf("resumed run %s", id)
	}
	if rerr != nil {
		out.Error(fmt.Errorf("resume: %w", rerr))
	}
	srv := &http.Server{Handler: Handler(r, opts), ReadHeaderTimeout: readHeaderTimeout}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	out.Printf("serving on http://%s", ln.Addr())

	select {
	case err := <-served:
		_ = r.Shutdown(context.Background())
		return err
	case <-ctx.Done():
	}
	out.Printf("-- stopping: runs stay unfinished for the next serve to resume")
	sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	// The runner first, which closes the event streams; the server's Shutdown waits for them.
	err = errors.Join(r.Shutdown(sctx), srv.Shutdown(sctx))
	<-served // http.ErrServerClosed, once Shutdown has begun
	return err
}
