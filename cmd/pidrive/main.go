// Command pidrive validates the Pi adapter against a live model. It opens one Pi session, runs
// one exchange to completion, runs a second exchange and cancels it mid-stream, and prints every
// normalized event tagged with its session and exchange IDs.
//
// Pi reaches the llama.cpp router through LLAMA_BASE_URL.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/JaimeStill/spike-harness-driver/harness"
	"github.com/JaimeStill/spike-harness-driver/pi"
)

func main() {
	provider := flag.String("provider", "llama.cpp", "Pi model provider")
	model := flag.String("model", "unsloth/gpt-oss-120b-GGUF:Q4_K_M", "model ID")
	cancelAfter := flag.Int("cancel-after", 5, "text deltas before the second exchange is cancelled")
	all := flag.Bool("all", false, "also print harness events with no normalized meaning")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, *provider, *model, *cancelAfter, *all); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, provider, model string, cancelAfter int, all bool) error {
	openCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s, err := pi.Driver{}.Open(openCtx, harness.Options{Provider: provider, Model: model})
	if err != nil {
		return err
	}
	defer func() {
		if err := s.Close(); err != nil {
			log.Printf("close: %v", err)
		}
	}()
	fmt.Printf("session %s\n", s.ID())

	exchanges := []struct {
		prompt      string
		cancelAfter int
	}{
		{prompt: "Reply with one short sentence: what is Go?"},
		{prompt: "Write a 600-word essay about the history of the Unix operating system.", cancelAfter: cancelAfter},
	}
	for _, e := range exchanges {
		x, err := s.Send(ctx, harness.Request{Text: e.prompt})
		if err != nil {
			return err
		}
		fmt.Printf("\nexchange %s: %q\n", x.ID(), e.prompt)
		deltas := 0
		for ev := range x.Events() {
			if ev.Kind == harness.KindHarness && !all {
				continue
			}
			printEvent(ev)
			if ev.Kind == harness.KindTextDelta {
				if deltas++; deltas == e.cancelAfter {
					fmt.Printf("-- cancelling after %d text deltas\n", deltas)
					x.Cancel()
				}
			}
		}
		res, err := x.Wait()
		fmt.Printf("result: stop=%s usage=%d/%d err=%v\n  text: %q\n",
			res.StopReason, res.Usage.Input, res.Usage.Output, err, res.Text)
	}
	return nil
}

func printEvent(ev harness.Event) {
	detail := ev.Text
	switch {
	case ev.Kind == harness.KindHarness:
		detail = string(ev.Raw)
		if len(detail) > 80 {
			detail = detail[:80] + "…"
		}
	case ev.Kind == harness.KindMessageEnd || ev.Kind == harness.KindEnded || ev.Kind == harness.KindCancelled:
		detail = "stop=" + ev.StopReason
	case ev.Err != "":
		detail = ev.Err
	case ev.Tool != nil:
		detail = ev.Tool.Name
	}
	fmt.Printf("%s %s %4d %-14s %q\n", short(ev.SessionID), short(ev.ExchangeID), ev.Seq, ev.Kind, detail)
}

// short keeps an ID's last eight characters, which differ between IDs made in the same
// millisecond.
func short(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[len(id)-8:]
}
