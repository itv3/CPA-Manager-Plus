// Package pricing converts token aggregates into monetary cost given a model price book.
package pricing

import "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"

// PerMillion divides by one million to convert token-priced units (per 1M tokens).
const PerMillion = 1_000_000.0

// ModelTokens represents the token totals consumed by a single model.
type ModelTokens struct {
	InputTokens  int64
	OutputTokens int64
	CachedTokens int64
}

// CostForModel computes the dollar cost for a single (model, tokens) pair.
// Cached tokens use the cache price (often discounted); when a model has no
// entry in the price book the cost is reported as zero.
func CostForModel(modelName string, tokens ModelTokens, prices map[string]model.ModelPrice) float64 {
	price, ok := prices[modelName]
	if !ok {
		return 0
	}
	return float64(tokens.InputTokens)*price.Prompt/PerMillion +
		float64(tokens.OutputTokens)*price.Completion/PerMillion +
		float64(tokens.CachedTokens)*price.Cache/PerMillion
}

// SumCost folds CostForModel over a slice of (model, tokens) tuples.
type Item struct {
	Model  string
	Tokens ModelTokens
}

// SumCost adds up the cost across multiple items.
func SumCost(items []Item, prices map[string]model.ModelPrice) float64 {
	total := 0.0
	for _, item := range items {
		total += CostForModel(item.Model, item.Tokens, prices)
	}
	return total
}
