# Standards

This file lists, one line each, the judgement calls the standards-reviewer applies that
`mise run check` can't enforce. It never restates what the check enforces.

These pages in the architecture repository apply:

- `principles/comments.md`: applies to Go source and tests, TypeScript, scripts, and every
  configuration file (TOML, `.gitignore`, `.dockerignore`, the Containerfile).
- `principles/minimal-footprint.md`
- `principles/rolling-currency.md`
- `principles/validation-first.md`

## Dependencies and references

- The repositories in `references.toml` are read-only: the spike reads them and never writes to
  them.
- Dependencies are published versions, never a `replace` directive.
- The spike serves the `standards-lab` coordinator and never edits it.
