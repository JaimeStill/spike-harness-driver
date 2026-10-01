// Package scenario runs clutch's narrated scenarios. Each scenario shows one capability of the
// harness driver on its own: it states what must be in place, then runs its steps in order,
// saying what each is about to do before doing it and reporting what it observed.
//
// A Scenario's Steps builds the steps of one run, so the steps can share that run's state,
// such as an open session, and the cleanup that releases it whatever step fails. The
// scenarios are defined beside the runner: exchange.go, cancel.go, resume.go, tool.go,
// skill.go, and structured.go. The tool and skill scenarios bring their own tool and skill,
// and each has a further step for the examples in clutch/examples, which runs when --tools or
// --skills loaded them.
//
// The capability scenarios, vision.go, embed.go, and audio.go, also call a model directly,
// through the Models the composition root sets up for one target, since the harness takes
// images but has nothing for embeddings or audio. They send the fixtures in
// clutch/examples/media and check the answers against what the fixtures are known to hold.
package scenario
