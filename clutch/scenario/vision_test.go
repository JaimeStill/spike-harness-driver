package scenario

import "testing"

func TestNamesShapes(t *testing.T) {
	pass := []string{
		"A red circle, a blue square, and a green triangle.",
		"The circle is red, the square is blue, and the triangle is green.",
		"| Shape | Color |\n|---|---|\n| Circle | Red |\n| Square | Blue |\n| Triangle | Green |",
		"1. **Circle** – red\n2. **Square** – blue\n3. **Triangle** – green",
		"Circle: RED\nSquare: BLUE\nTriangle: GREEN",
		"The circle, which is red, sits left of a blue square; a green triangle is on a white background.",
		"There are red circles, blue squares, and green triangles.",
	}
	for _, reply := range pass {
		if err := namesShapes(reply); err != nil {
			t.Errorf("%q: %v", reply, err)
		}
	}
	fail := []string{
		"",
		"(image omitted: model does not support images) I can't see any image.",
		"A blue circle, a red square, and a green triangle.",
		"A red circle and a blue square.",
		"Circle, square, triangle.",
		"The image shows red, blue, and green.",
	}
	for _, reply := range fail {
		if err := namesShapes(reply); err == nil {
			t.Errorf("%q passed", reply)
		}
	}
}
