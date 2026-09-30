// Package media holds the media fixtures the capability scenarios send to a model, embedded so
// a scenario finds them whatever its working directory. README.md says how each was made.
package media

import _ "embed"

// Shapes is shapes.png: a red circle, a blue square, and a green triangle on white, 480 pixels
// square.
//
//go:embed shapes.png
var Shapes []byte

// Phrase is phrase.wav: one speaker saying "The access code is seven four two nine.", 16 kHz
// mono and 6 seconds long.
//
//go:embed phrase.wav
var Phrase []byte
