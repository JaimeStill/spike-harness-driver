// Command clutch drives an external agent harness from Go. This file is only the process's
// entry point: it derives the root context from the interrupt signal, hands the process's
// streams to the composition root, and exits with the code the run returns. It imports only
// clutch/internal/app.
//
// The signal context comes from the standard library, which keeps go-core out of the spike's
// dependencies.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/JaimeStill/spike-harness-driver/clutch/internal/app"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return app.New(os.Stdout, os.Stderr).Run(ctx)
}
