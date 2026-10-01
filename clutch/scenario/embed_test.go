package scenario

import (
	"math"
	"slices"
	"testing"
)

func TestCosine(t *testing.T) {
	cases := []struct {
		a, b []float32
		want float64
	}{
		{[]float32{1, 0}, []float32{1, 0}, 1},
		{[]float32{1, 0}, []float32{0, 1}, 0},
		{[]float32{1, 0}, []float32{-2, 0}, -1},
		{[]float32{3, 4}, []float32{6, 8}, 1},
		{[]float32{1, 1}, []float32{1, 0}, 1 / math.Sqrt2},
	}
	for _, c := range cases {
		got, err := cosine(c.a, c.b)
		if err != nil || math.Abs(got-c.want) > 1e-12 {
			t.Errorf("cosine(%v, %v) = %v, %v; want %v", c.a, c.b, got, err, c.want)
		}
	}
	if _, err := cosine([]float32{1, 0}, []float32{1}); err == nil {
		t.Error("vectors of different lengths compared")
	}
	if _, err := cosine([]float32{0, 0}, []float32{1, 0}); err == nil {
		t.Error("a zero vector compared")
	}
}

func TestRank(t *testing.T) {
	query := []float32{1, 0, 0}
	order, scores, err := rank(query, [][]float32{{0, 1, 0}, {1, 0.1, 0}, {0.5, 0.5, 0}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(order, []int{1, 2, 0}) {
		t.Errorf("order = %v", order)
	}
	if scores[0] != 0 || scores[1] <= scores[2] {
		t.Errorf("scores = %v", scores)
	}
	if _, _, err := rank(query, [][]float32{{1, 0}}); err == nil {
		t.Error("a mismatched candidate ranked")
	}
}

func TestTheHoneyPassageComesFirst(t *testing.T) {
	// The embed scenario checks that passages[0] ranks first, so it must be the honey one.
	if passages[0].label != "honey" || len(passages) != 3 {
		t.Errorf("passages = %+v", passages)
	}
}
