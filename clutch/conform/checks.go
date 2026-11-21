package conform

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/JaimeStill/spike-harness-driver/clutch/scenario"
	"github.com/JaimeStill/spike-harness-driver/harness"
)

func decode(structured json.RawMessage, v any) error {
	if len(structured) == 0 {
		return errors.New("the exchange ended without a structured response")
	}
	if err := json.Unmarshal(structured, v); err != nil {
		return fmt.Errorf("decode the structured response %s: %w", structured, err)
	}
	return nil
}

func fold(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func sameValue(what, got, want string) error {
	if fold(got) != fold(want) {
		return fmt.Errorf("%s is %q, want %q", what, got, want)
	}
	return nil
}

func sameMotto(got, want string) error {
	if scenario.Normalize(got) != scenario.Normalize(want) {
		return fmt.Errorf("the motto is %q, want %q", got, want)
	}
	return nil
}

func checkCapital(city, country string) error {
	return errors.Join(sameValue("the city", city, "Paris"), sameValue("the country", country, "France"))
}

var primaryColors = []string{"blue", "red", "yellow"}

func checkColors(count int, colors []string) error {
	var errs []error
	if count != len(primaryColors) {
		errs = append(errs, fmt.Errorf("count is %d, want %d", count, len(primaryColors)))
	}
	got := make([]string, len(colors))
	for i, c := range colors {
		got[i] = fold(c)
	}
	slices.Sort(got)
	if !slices.Equal(slices.Compact(got), primaryColors) {
		errs = append(errs, fmt.Errorf("the colors are %q, want %q as a set", colors, primaryColors))
	}
	return errors.Join(errs...)
}

var fixtureShapes = []string{"circle red", "square blue", "triangle green"}

func shapeKey(s shapeColor) string {
	shape := strings.TrimSuffix(fold(s.Shape), "s")
	if shape == "rectangle" {
		shape = "square"
	}
	return shape + " " + fold(s.Color)
}

func checkShapes(got []shapeColor) error {
	keys := make([]string, len(got))
	for i, s := range got {
		keys[i] = shapeKey(s)
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)
	want := slices.Clone(fixtureShapes)
	slices.Sort(want)
	if slices.Equal(keys, want) {
		return nil
	}
	var missing, extra []string
	for _, w := range want {
		if !slices.Contains(keys, w) {
			missing = append(missing, w)
		}
	}
	for _, k := range keys {
		if !slices.Contains(want, k) {
			extra = append(extra, k)
		}
	}
	return fmt.Errorf("the shapes differ from the fixture's: missing [%s], extra [%s]", strings.Join(missing, "; "), strings.Join(extra, "; "))
}

func toolRan(events []harness.Event, name string) error {
	var called, returned bool
	for _, ev := range events {
		if ev.Tool == nil || ev.Tool.Name != name {
			continue
		}
		called = called || ev.Kind == harness.EventToolCall
		returned = returned || ev.Kind == harness.EventToolResult
	}
	switch {
	case !called:
		return fmt.Errorf("the model never called the %s tool", name)
	case !returned:
		return fmt.Errorf("the %s tool was called but no result came back", name)
	}
	return nil
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[len(id)-8:]
}
