package scenario

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/JaimeStill/spike-harness-driver/model"
)

// embedQuery is the embed scenario's query.
const embedQuery = "How do bees make honey?"

// queryForm and documentForm are the prompt forms EmbeddingGemma 2's model card gives for
// question answering: a query names the task, and a document without a title says so. A model
// trained without prompt forms, such as OpenAI's, embeds the prefix as more text, which leaves
// the ranking unharmed.
const (
	queryForm    = "task: question answering | query: %s"
	documentForm = "title: none | text: %s"
)

// embedInput is the embed scenario's one request's input: the query, then each passage, each in
// its prompt form.
func embedInput() []string {
	input := []string{fmt.Sprintf(queryForm, embedQuery)}
	for _, p := range passages {
		input = append(input, fmt.Sprintf(documentForm, p.text))
	}
	return input
}

// passage is a text the embed scenario ranks against its query, with a short label to print.
type passage struct {
	label, text string
}

// passages are the embed scenario's candidates. The first answers the query, and the ranking
// must put it first.
var passages = []passage{
	{"honey", "Honeybees collect nectar from flowers, carry it back to the hive, and fan it with their wings until the water evaporates and it thickens into honey."},
	{"volcanoes", "Volcanoes form where magma rises through the Earth's crust, erupting as lava, ash, and gas when the pressure beneath grows too great."},
	{"stocks", "Stock market prices move as investors buy and sell shares, reacting to company earnings, interest rates, and economic news."},
}

// embedScenario embeds a query and three passages with the direct client, in one request, and
// ranks the passages by their similarity to the query. It needs no harness: the harness has
// nothing for embeddings.
func embedScenario(models func() (Models, error), needs []Need) Scenario {
	return Scenario{
		Name:    "embed",
		Summary: "A query and three passages embedded by the direct client, the passage that answers ranked first",
		Needs:   needs,
		Steps: func() ([]Step, func() error) {
			return []Step{
				{
					Intent: "Embed the query and three passages in one request, and rank the passages by cosine similarity",
					Action: func(ctx context.Context, rep *Reporter) error {
						m, err := models()
						if err != nil {
							return err
						}
						rep.Note("model %s on target %s", m.EmbedModel, m.Target)
						resp, err := m.Chat.Embed(ctx, model.EmbedRequest{Model: m.EmbedModel, Input: embedInput()})
						if err != nil {
							return err
						}
						rep.Note("%d vectors of %d dimensions, %d input tokens", len(resp.Vectors), len(resp.Vectors[0]), resp.Usage.InputTokens)
						order, scores, err := rank(resp.Vectors[0], resp.Vectors[1:])
						if err != nil {
							return err
						}
						for i, p := range order {
							rep.Note("%d. %-10s %.4f", i+1, passages[p].label, scores[p])
						}
						if order[0] != 0 {
							return fmt.Errorf("the %s passage ranks first, want the %s passage", passages[order[0]].label, passages[0].label)
						}
						return nil
					},
				},
			}, nil
		},
	}
}

// rank orders candidates by their cosine similarity to query, most similar first. It returns
// the candidates' indexes in that order, and each candidate's score by its own index.
func rank(query []float32, candidates [][]float32) (order []int, scores []float64, err error) {
	scores = make([]float64, len(candidates))
	order = make([]int, len(candidates))
	for i, c := range candidates {
		if scores[i], err = cosine(query, c); err != nil {
			return nil, nil, fmt.Errorf("candidate %d: %w", i, err)
		}
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return scores[order[a]] > scores[order[b]] })
	return order, scores, nil
}

// cosine is the cosine similarity of a and b. It accumulates in float64, since a sum of a few
// thousand float32 products loses precision in float32.
func cosine(a, b []float32) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("vectors of %d and %d dimensions", len(a), len(b))
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0, errors.New("a zero vector has no direction")
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb)), nil
}
