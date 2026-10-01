package scenario

// Profile is how one harness differs where a scenario's steps depend on it: what the harness
// calls its tool for loading a skill, how a prompt invokes a skill directly, and whether it
// drops an image a model can't take. A scenario reads the profile when it runs, so its code
// names no harness.
type Profile struct {
	// Name is the harness as the narration calls it.
	Name string
	// SkillTool is the harness tool that lets the model load a skill, which must be among a
	// session's HarnessTools for the model to find a skill itself.
	SkillTool string
	// SkillPrefix, followed by a skill's name and the rest of the prompt, has the harness
	// inline the skill with no tool at all.
	SkillPrefix string
	// DropsImage reports whether the harness, given an image for a model that takes none,
	// drops it and runs the exchange anyway. A harness whose models all take images has no
	// such case to show.
	DropsImage bool
}

// Pi and Claude are the profiles of the harnesses clutch drives.
var (
	Pi = Profile{
		Name:        "Pi",
		SkillTool:   "read",
		SkillPrefix: "/skill:",
		DropsImage:  true,
	}
	Claude = Profile{
		Name:        "Claude Code",
		SkillTool:   "Skill",
		SkillPrefix: "/driver:",
	}
)
