package app

import "testing"

func TestValidateLoadsTheSourcesIntoEverySessionsOptions(t *testing.T) {
	infra := newInfrastructure(&Config{
		Harness: "pi",
		State:   t.TempDir(),
		Tools:   []string{"../../examples/tools"},
		Skills:  []string{"../../examples/skills"},
	})
	if opts := infra.Options(); len(opts.Tools) != 0 || len(opts.Skills) != 0 {
		t.Fatalf("options before Validate: %+v", opts)
	}
	if err := infra.Validate(); err != nil {
		t.Fatal(err)
	}
	opts := infra.Options()
	if len(opts.Tools) != 1 || opts.Tools[0].Name != "fingerprint" || opts.Tools[0].Handler == nil {
		t.Errorf("tools = %+v", opts.Tools)
	}
	if len(opts.Skills) != 1 || opts.Skills[0].Name != "clutch-weather" {
		t.Errorf("skills = %+v", opts.Skills)
	}
	// The session domain reads the same options, so session send offers them too.
	svc := newDomain(infra).Session
	if len(svc.Tools()) != 1 || len(svc.Skills()) != 1 {
		t.Errorf("session domain offers %d tools, %d skills", len(svc.Tools()), len(svc.Skills()))
	}
}

func TestTheExampleToolRuns(t *testing.T) {
	infra := newInfrastructure(&Config{Harness: "pi", Tools: []string{"../../examples/tools"}})
	if err := infra.Validate(); err != nil {
		t.Fatal(err)
	}
	got, err := infra.Options().Tools[0].Handler(t.Context(), []byte(`{"text":"clutch"}`))
	if err != nil {
		t.Skipf("fingerprint needs python3: %v", err)
	}
	if got != "c278be9e2247\n" {
		t.Errorf("fingerprint(clutch) = %q", got)
	}
}
