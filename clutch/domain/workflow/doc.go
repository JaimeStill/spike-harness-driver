// Package workflow is clutch's workflow domain, whose subject is a run of a workflow: a DAG of
// exchanges over several harness sessions, which the workflow package's Runner runs and logs.
//
// The Service builds a Runner over the session opener and run log that the composition root
// supplies, and Follow passes a run's events to a function as they happen. Commands mounts the
// "workflow" commands: run starts a run from a workflow file and follows it to its end, resume
// takes up a run that a previous process left unfinished, cancel ends such a run, list lists the
// runs, and show prints one run's state. An interrupt stops a run without ending it, so resume
// can take it up.
//
// Handler serves the same runs over HTTP, with each run's events as Server-Sent Events.
// ServeCommand mounts "serve", which hosts the Handler and, at start, resumes the runs a
// previous process left unfinished.
package workflow
