// Package workflow is clutch's workflow domain. Its root is a run of a workflow: a DAG of
// exchanges over several harness sessions, which the workflow package's Runner runs and logs.
//
// The Service builds a Runner over the session opener and run log the composition root
// supplies, and Follow prints a run's events as they happen. Commands mounts the direct
// commands under "workflow": run, which starts a run from a workflow file and follows it to its
// end; resume, which takes up a run a previous process left unfinished; cancel, which ends such
// a run; list; and show. An interrupt stops a run without ending it, so resume can take it up.
//
// Handler serves the same runs over HTTP, with each run's events as Server-Sent Events, and
// ServeCommand mounts "serve", which hosts it and resumes at start the runs a previous process
// left unfinished.
package workflow
