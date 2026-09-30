package scenario

import "github.com/JaimeStill/spike-harness-driver/model"

// Models is the direct model client the capability scenarios call, set up for one target, and
// the model a harness session that takes images runs on.
type Models struct {
	// Target names the endpoint the clients talk to, such as "llama.cpp" or "azure".
	Target string
	// Chat serves chat and embeddings.
	Chat *model.Client
	// Audio serves transcription. It is Chat itself on a target that serves every route from
	// one base URL.
	Audio *model.Client
	// VisionModel, EmbedModel, and AudioModel are the model IDs each kind of request names.
	VisionModel string
	EmbedModel  string
	AudioModel  string
	// AudioInChat reports whether the target's audio model takes audio in a chat request, as
	// well as through transcription.
	AudioInChat bool
	// HarnessVision is the model a harness session runs on when it is sent an image. The
	// harness keeps its own provider, whatever the target.
	HarnessVision string
}

// Needs is what each kind of scenario checks before its first step.
type Needs struct {
	// Harness is what a scenario that drives only the harness needs.
	Harness []Need
	// Models is what a scenario that calls only the direct model client needs.
	Models []Need
	// Both is what a scenario that does both needs, with a need the two share listed once.
	Both []Need
}
