// classifier.go — taxonomy classification of discourse items via the FAST lane.
//
// Purpose:    BatchClassify tags a slice of DiscourseItems with ppi:* taxonomy
//             tags using the gateway FAST lane. Items are batched at most 20 per
//             LLM call. Every item receives at least one ppi:* tag (a deterministic
//             fallback guarantees this when the model returns nothing usable).
// Inputs:     []DiscourseItem + the allowed taxonomy tag set.
// Outputs:    the same items with Tags populated (≥1 ppi:* each).
// Constraints: File ≤500 lines. The LLM call is an interface seam (Completer);
//             tests inject a stub — no network.
//
// SPORT: REGISTRY-FUNCTIONS.md → discourse.BatchClassify.
package discourse

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// maxBatch caps items per LLM classification call (canonical 20).
const maxBatch = 20

// ppiPrefix is the required taxonomy namespace prefix for every tag.
const ppiPrefix = "ppi:"

// fallbackTag is applied when the model returns no valid ppi:* tag for an item.
const fallbackTag = "ppi:uncategorized"

// LaneRequest is the minimal completion request the classifier sends. It mirrors
// the gateway's fast-lane shape without importing the gateway (avoids a cycle):
// callers adapt their gateway to the Completer seam.
type ClassifyRequest struct {
	SystemPrompt string
	Prompt       string
}

// ClassifyResponse is the model's text reply.
type ClassifyResponse struct {
	Content string
}

// Completer is the FAST-lane seam. The production wiring adapts the gateway's
// fast-lane Complete; tests inject a stub returning canned JSON.
type Completer interface {
	Complete(ctx context.Context, req ClassifyRequest) (*ClassifyResponse, error)
}

// Classifier tags discourse items with ppi:* taxonomy labels.
type Classifier struct {
	fast Completer
}

// NewClassifier wires the FAST-lane completer.
func NewClassifier(fast Completer) *Classifier {
	return &Classifier{fast: fast}
}

// BatchClassify tags items in batches of ≤maxBatch, guaranteeing ≥1 ppi:* tag
// per item. taxonomyAllowed restricts which tags the model may apply; tags not in
// the set are dropped, and items left with no valid tag get fallbackTag.
func (c *Classifier) BatchClassify(ctx context.Context, items []DiscourseItem, taxonomyAllowed []string) ([]DiscourseItem, error) {
	allowed := map[string]bool{}
	for _, t := range taxonomyAllowed {
		allowed[t] = true
	}
	for start := 0; start < len(items); start += maxBatch {
		end := start + maxBatch
		if end > len(items) {
			end = len(items)
		}
		batch := items[start:end]
		tags, err := c.classifyBatch(ctx, batch, taxonomyAllowed)
		if err != nil {
			return nil, err
		}
		for i := range batch {
			applied := filterTags(tags[i], allowed)
			if len(applied) == 0 {
				applied = []string{fallbackTag}
			}
			items[start+i].Tags = applied
		}
	}
	return items, nil
}

// filterTags keeps only valid ppi:* tags present in the allowed set.
func filterTags(tags []string, allowed map[string]bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if !strings.HasPrefix(t, ppiPrefix) || seen[t] {
			continue
		}
		// Empty allowed set ⇒ accept any ppi:* tag.
		if len(allowed) > 0 && !allowed[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// classifyBatch issues one FAST-lane call for a ≤maxBatch slice and parses the
// JSON array response into per-item tag slices.
func (c *Classifier) classifyBatch(ctx context.Context, batch []DiscourseItem, taxonomy []string) ([][]string, error) {
	if len(batch) > maxBatch {
		return nil, fmt.Errorf("classifier: batch size %d exceeds max %d", len(batch), maxBatch)
	}
	prompt := buildPrompt(batch, taxonomy)
	resp, err := c.fast.Complete(ctx, ClassifyRequest{
		SystemPrompt: systemPrompt,
		Prompt:       prompt,
	})
	if err != nil {
		return nil, fmt.Errorf("classifier: fast lane: %w", err)
	}
	tags, perr := parseTags(resp.Content, len(batch))
	if perr != nil {
		// Degrade gracefully: empty tags → fallback applied by caller.
		return make([][]string, len(batch)), nil
	}
	return tags, nil
}

const systemPrompt = "You classify software-project discourse (issues, PRs, commits, review comments) " +
	"into taxonomy tags. Reply ONLY with a JSON array; element i is an array of tag strings for item i. " +
	"Use ONLY tags from the provided allowed list. Every item must get at least one tag."

// buildPrompt renders the batch + allowed taxonomy into a single FAST-lane prompt.
func buildPrompt(batch []DiscourseItem, taxonomy []string) string {
	var b strings.Builder
	b.WriteString("Allowed tags: ")
	b.WriteString(strings.Join(taxonomy, ", "))
	b.WriteString("\n\nItems:\n")
	for i, it := range batch {
		body := it.Body
		if len(body) > 800 {
			body = body[:800]
		}
		fmt.Fprintf(&b, "%d. [%s] %s\n%s\n\n", i, it.Kind, it.Title, body)
	}
	b.WriteString(fmt.Sprintf("Return a JSON array of %d tag-arrays.", len(batch)))
	return b.String()
}

// parseTags extracts the JSON array-of-arrays from a (possibly fenced) model
// reply and validates length matches the batch.
func parseTags(content string, n int) ([][]string, error) {
	s := strings.TrimSpace(content)
	// Strip markdown code fences if present.
	if i := strings.Index(s, "["); i >= 0 {
		if j := strings.LastIndex(s, "]"); j >= i {
			s = s[i : j+1]
		}
	}
	var tags [][]string
	if err := json.Unmarshal([]byte(s), &tags); err != nil {
		return nil, fmt.Errorf("classifier: parse tags: %w", err)
	}
	if len(tags) != n {
		return nil, fmt.Errorf("classifier: got %d tag-arrays, want %d", len(tags), n)
	}
	return tags, nil
}
