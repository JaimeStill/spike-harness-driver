package conform

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

// Render writes the matrix, the reason for every cell that isn't a pass, and a summary line.
// The matrix has a row per harness and provider, with the harness's version, and a column per
// capability; a skipped cell reads skip all the way across.
func Render(w io.Writer, reports []Report) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	header := []string{"HARNESS", "PROVIDER", "VERSION"}
	for _, c := range Capabilities {
		header = append(header, c.Name)
	}
	_, _ = fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, r := range reports {
		row := []string{r.Harness, r.Provider, r.Version}
		for i := range Capabilities {
			status := Skip
			if i < len(r.Results) {
				status = r.Results[i].Status
			}
			row = append(row, status.String())
		}
		_, _ = fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	_ = tw.Flush()
	renderLatency(w, reports)

	var wrote bool
	for _, r := range reports {
		if r.Skipped != "" {
			wrote = reasonHeading(w, wrote)
			_, _ = fmt.Fprintf(w, "  %s  skip  %s\n", r.Label(), oneLine(r.Skipped))
		}
		for _, res := range r.Results {
			if res.Status == Pass {
				continue
			}
			wrote = reasonHeading(w, wrote)
			_, _ = fmt.Fprintf(w, "  %s  %s  %s: %s\n", r.Label(), res.Status, res.Capability, oneLine(res.Reason))
		}
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, Summary(reports))
}

// renderLatency writes the latency the exchange capability measured on each cell that has it,
// one sample each: the session's open, which starts the harness, and the plain exchange's first
// text and end, from sending the prompt.
func renderLatency(w io.Writer, reports []Report) {
	var rows []Report
	for _, r := range reports {
		if r.Latency != nil {
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "\nLatency of one plain exchange:\n")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  HARNESS\tPROVIDER\tOPEN\tFIRST TEXT\tTURN")
	for _, r := range rows {
		l := r.Latency
		_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", r.Harness, r.Provider, seconds(l.Open), seconds(l.FirstText), seconds(l.Turn))
	}
	_ = tw.Flush()
}

// seconds renders d in seconds to a tenth, or "-" for none.
func seconds(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// reasonHeading writes the heading over the reasons once, and returns true.
func reasonHeading(w io.Writer, wrote bool) bool {
	if !wrote {
		_, _ = fmt.Fprintf(w, "\nWhy not pass:\n")
	}
	return true
}

// Summary is the one line that says how the run went: the cells that ran and were skipped, and
// the capabilities' statuses across the cells that ran.
func Summary(reports []Report) string {
	var ran, skipped int
	counts := map[Status]int{}
	for _, r := range reports {
		if r.Skipped != "" {
			skipped++
			continue
		}
		ran++
		for _, res := range r.Results {
			counts[res.Status]++
		}
	}
	return fmt.Sprintf("%d cells ran, %d skipped; %d pass, %d FAIL, %d n/a",
		ran, skipped, counts[Pass], counts[Fail], counts[NA])
}

// Failures counts the capabilities that failed across reports. A skipped cell fails none.
func Failures(reports []Report) int {
	n := 0
	for _, r := range reports {
		n += r.Failures()
	}
	return n
}

// oneLine puts a reason on one line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
