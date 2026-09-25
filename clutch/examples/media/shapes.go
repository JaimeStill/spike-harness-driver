//go:build ignore

// Generates shapes.png: a red circle, a blue square, and a green triangle on white.
// Run from the repository root: go run ./clutch/examples/media/shapes.go
package main

import (
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
)

func main() {
	const size = 480
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	white := color.RGBA{255, 255, 255, 255}
	red := color.RGBA{220, 30, 30, 255}
	blue := color.RGBA{30, 60, 220, 255}
	green := color.RGBA{30, 170, 60, 255}

	for y := range size {
		for x := range size {
			img.Set(x, y, white)
			switch {
			case circle(x, y, 120, 130, 80):
				img.Set(x, y, red)
			case x >= 280 && x < 420 && y >= 60 && y < 200:
				img.Set(x, y, blue)
			case triangle(x, y, 240, 260, 160):
				img.Set(x, y, green)
			}
		}
	}

	f, err := os.Create("clutch/examples/media/shapes.png")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
}

func circle(x, y, cx, cy, r int) bool {
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}

// triangle reports whether (x, y) is inside the upward triangle with its apex at (ax, ay)
// and height h, whose base is as wide as it is tall.
func triangle(x, y, ax, ay, h int) bool {
	dy := y - ay
	if dy < 0 || dy > h {
		return false
	}
	half := dy / 2
	return x >= ax-half && x <= ax+half
}
