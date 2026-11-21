# Standards

The judgement calls the check can't enforce, one line each, for the standards-reviewer. What
`mise run check` enforces is never restated here.

The architecture pages that apply, in the architecture repository:

- `principles/comments.md`: every comment is one of the five kinds, in Go source and tests,
  TypeScript, scripts, and every configuration file (TOML, `.gitignore`, `.dockerignore`, the
  Containerfile). In Go, package comments and exported identifiers' doc comments are its package
  documentation; a script's header is its package documentation, and a configuration file's
  header is a short description plus `See` lines to the page that holds its reasoning.
- `principles/minimal-footprint.md`
- `principles/rolling-currency.md`
- `principles/validation-first.md`

## Dependencies and references

- The repositories in `references.toml` are read-only: the spike reads them and never writes to
  them.
- Dependencies are published versions, never a `replace` directive.
- The spike serves the `standards-lab` coordinator and never edits it.
