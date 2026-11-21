# Standards

This file lists, one line each, the judgement calls the standards-reviewer applies that
`mise run check` can't enforce. It never restates what the check enforces.

These pages in the architecture repository apply:

- `principles/comments.md`: every comment is one of the five kinds, in Go source and tests,
  TypeScript, scripts, and every configuration file (TOML, `.gitignore`, `.dockerignore`, the
  Containerfile). In Go, the package comment and the exported identifiers' doc comments are the
  package documentation. A script's header is its package documentation, and a configuration
  file's header is a short description plus `See` lines to the page that holds its reasoning.
- `principles/minimal-footprint.md`
- `principles/rolling-currency.md`
- `principles/validation-first.md`

## Dependencies and references

- The repositories in `references.toml` are read-only: the spike reads them and never writes to
  them.
- Dependencies are published versions, never a `replace` directive.
- The spike serves the `standards-lab` coordinator and never edits it.
