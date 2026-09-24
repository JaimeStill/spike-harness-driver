// Package catalog loads the tools and skills a session offers from outside the program: skill
// trees on disk or embedded in a Go library, and command tools on disk. A Go library that
// packages its own tools needs none of this; it builds harness.Tool values with Go handlers.
//
// A skill is a directory holding a SKILL.md whose frontmatter names and describes it, the
// Agent Skills layout Pi reads. Skill loads one such directory and Skills loads every one
// directly under a root. Both take an fs.FS, so a skill tree may be an os.DirFS or an fs.Sub of
// an embed.FS, and the harness.Skill keeps that FS for the adapter to hand to the harness.
//
// A command tool is a directory holding a tool.json manifest and the executable it names. The
// manifest's inputSchema field takes MCP's name for the argument schema, so a manifest can map
// to an MCP tool later. Each call runs the command in the tool's directory with the arguments
// on stdin and returns its stdout. CommandTool and Tools take an OS directory rather than an
// fs.FS, because an embedded file can't be executed.
package catalog
