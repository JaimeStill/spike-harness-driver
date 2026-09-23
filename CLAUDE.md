# spike-harness-driver

A spike that tests whether Go can drive an external agent harness as the infrastructure for
agentic work. It is a `code` project run with the marathon workflow. Start from
`context/README.md`.

The spike serves the Standards Lab workspace's `standards-lab` coordinator
(`github.com/standards-lab/org`) and never edits it. The repositories listed in
`references.toml` are read-only: read them, and never write to them. Dependencies are published
versions, never a `replace` directive.
