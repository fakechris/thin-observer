// Package lineage: heuristic lineage inference across two snapshots of a plan.
//
// The inferrer is pure: given old tasks and new task items, it emits Decisions.
// All I/O is done by the caller (ingest). The five passes run in strict order:
//
//  1. Exact match (title+phase)                → SameTask,   confidence 1.0
//  2. Alias match (new.title ∈ old.aliases)    → SameTask,   confidence 0.95
//  3. Rename    (Levenshtein ratio ≥ rename)   → Rename,     confidence = ratio
//  4. Split     (1 old, 2+ new, token overlap) → Split,      confidence = overlap
//  5. Merge     (2+ old, 1 new, token overlap) → Merge,      confidence = overlap
//  6. Remaining new items                      → NewTask,    confidence 1.0
//
// Unmatched old tasks are NOT reported here — they become missing_in_rev bumps
// in the ingester, which eventually marks them lost.
package lineage

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/agnivade/levenshtein"
)

// Options tune the heuristics. Zero values give reasonable defaults.
type Options struct {
	RenameThreshold float64 // default 0.80 — ratio ≥ this → Rename
	SplitOverlap    float64 // default 0.40 — per-child token overlap
	MergeOverlap    float64 // default 0.40 — per-parent token overlap
}

// Heuristic implements Inferrer with Levenshtein + token-overlap heuristics.
type Heuristic struct{ Options }

// New returns a Heuristic with sensible defaults.
func New() *Heuristic {
	return &Heuristic{Options{
		RenameThreshold: 0.80,
		SplitOverlap:    0.40,
		MergeOverlap:    0.40,
	}}
}

// NewWithOptions returns a Heuristic with overrides applied to defaults.
func NewWithOptions(o Options) *Heuristic {
	h := New()
	if o.RenameThreshold > 0 {
		h.RenameThreshold = o.RenameThreshold
	}
	if o.SplitOverlap > 0 {
		h.SplitOverlap = o.SplitOverlap
	}
	if o.MergeOverlap > 0 {
		h.MergeOverlap = o.MergeOverlap
	}
	return h
}

// Infer is the entry point. It returns one decision per new item; split decisions
// consume multiple new indexes (ChildNewIndexes) and still appear once.
func (h *Heuristic) Infer(old []ExistingTask, newItems []NewItem) []Decision {
	usedOld := make(map[int]bool, len(old))
	usedNew := make(map[int]bool, len(newItems))
	decisions := make([]Decision, 0, len(newItems))

	// Pre-tokenize.
	oldTokens := make([]map[string]struct{}, len(old))
	for i, o := range old {
		oldTokens[i] = tokenSet(o.Title)
	}
	newTokens := make([]map[string]struct{}, len(newItems))
	for i, n := range newItems {
		newTokens[i] = tokenSet(n.Title)
	}

	// -- Pass 1: exact match (title + phase).
	for ni, n := range newItems {
		if usedNew[ni] {
			continue
		}
		for oi, o := range old {
			if usedOld[oi] {
				continue
			}
			if o.Title == n.Title && phaseEq(o.Phase, n.Phase) {
				decisions = append(decisions, Decision{
					Kind: DecisionSameTask, OldID: o.ID, NewIndex: ni, Confidence: 1.0,
				})
				usedOld[oi] = true
				usedNew[ni] = true
				break
			}
		}
	}

	// -- Pass 2: alias match (new.title ∈ old.aliases).
	for ni, n := range newItems {
		if usedNew[ni] {
			continue
		}
		for oi, o := range old {
			if usedOld[oi] {
				continue
			}
			if !phaseEq(o.Phase, n.Phase) {
				continue
			}
			if containsStr(o.Aliases, n.Title) {
				decisions = append(decisions, Decision{
					Kind: DecisionSameTask, OldID: o.ID, NewIndex: ni, Confidence: 0.95,
				})
				usedOld[oi] = true
				usedNew[ni] = true
				break
			}
		}
	}

	// -- Pass 3: rename — Levenshtein ratio ≥ threshold, same phase.
	// Build candidate pairs, sort by ratio desc, greedy assign.
	type cand struct {
		oi, ni int
		ratio  float64
	}
	var cands []cand
	for oi, o := range old {
		if usedOld[oi] {
			continue
		}
		for ni, n := range newItems {
			if usedNew[ni] {
				continue
			}
			if !phaseEq(o.Phase, n.Phase) {
				continue
			}
			r := ratio(o.Title, n.Title)
			if r >= h.RenameThreshold && r < 1.0 {
				cands = append(cands, cand{oi, ni, r})
			}
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].ratio > cands[j].ratio })
	for _, c := range cands {
		if usedOld[c.oi] || usedNew[c.ni] {
			continue
		}
		decisions = append(decisions, Decision{
			Kind: DecisionRename, OldID: old[c.oi].ID, NewIndex: c.ni, Confidence: c.ratio,
		})
		usedOld[c.oi] = true
		usedNew[c.ni] = true
	}

	// -- Pass 4: split (1 unused old → 2+ unused new, same phase, token overlap).
	for oi, o := range old {
		if usedOld[oi] {
			continue
		}
		if len(oldTokens[oi]) == 0 {
			continue
		}
		var children []int
		var overlapSum float64
		for ni, n := range newItems {
			if usedNew[ni] {
				continue
			}
			if !phaseEq(o.Phase, n.Phase) {
				continue
			}
			if len(newTokens[ni]) == 0 {
				continue
			}
			ov := jaccard(oldTokens[oi], newTokens[ni])
			// Child must share at least split threshold with parent; each child
			// should be "smaller" (more specific) than the umbrella.
			if ov >= h.SplitOverlap && len(n.Title) < len(o.Title)+20 {
				children = append(children, ni)
				overlapSum += ov
			}
		}
		if len(children) >= 2 {
			conf := overlapSum / float64(len(children))
			if conf > 0.9 {
				conf = 0.9 // cap: split is always below a high-confidence rename
			}
			decisions = append(decisions, Decision{
				Kind: DecisionSplit, OldID: o.ID,
				NewIndex: children[0], ChildNewIndexes: append([]int(nil), children...),
				Confidence: conf,
			})
			usedOld[oi] = true
			for _, c := range children {
				usedNew[c] = true
			}
		}
	}

	// -- Pass 5: merge (2+ unused old → 1 unused new, same phase, token overlap).
	for ni, n := range newItems {
		if usedNew[ni] {
			continue
		}
		if len(newTokens[ni]) == 0 {
			continue
		}
		var parents []int
		var overlapSum float64
		for oi, o := range old {
			if usedOld[oi] {
				continue
			}
			if !phaseEq(o.Phase, n.Phase) {
				continue
			}
			if len(oldTokens[oi]) == 0 {
				continue
			}
			ov := jaccard(oldTokens[oi], newTokens[ni])
			if ov >= h.MergeOverlap && len(o.Title) < len(n.Title)+20 {
				parents = append(parents, oi)
				overlapSum += ov
			}
		}
		if len(parents) >= 2 {
			conf := overlapSum / float64(len(parents))
			if conf > 0.9 {
				conf = 0.9
			}
			oldIDs := make([]string, len(parents))
			for i, oi := range parents {
				oldIDs[i] = old[oi].ID
			}
			decisions = append(decisions, Decision{
				Kind: DecisionMerge, OldIDs: oldIDs, NewIndex: ni, Confidence: conf,
			})
			usedNew[ni] = true
			for _, oi := range parents {
				usedOld[oi] = true
			}
		}
	}

	// -- Pass 6: remaining new → NewTask.
	for ni := range newItems {
		if usedNew[ni] {
			continue
		}
		decisions = append(decisions, Decision{
			Kind: DecisionNewTask, NewIndex: ni, Confidence: 1.0,
		})
	}

	// Keep decisions in order of NewIndex (ascending) for deterministic output,
	// but note: split Decision occupies only NewIndex = children[0]. Sort by
	// NewIndex for deterministic tests.
	sort.Slice(decisions, func(i, j int) bool {
		return decisions[i].NewIndex < decisions[j].NewIndex
	})
	return decisions
}

// ratio returns 1 - (distance / maxLen). Returns 1.0 when both strings are empty.
// Length is measured in runes, not bytes, so multi-byte UTF-8 characters
// (CJK, emoji, accented Latin) produce the same ratio regardless of encoding.
func ratio(a, b string) float64 {
	if a == "" && b == "" {
		return 1.0
	}
	if a == "" || b == "" {
		return 0.0
	}
	d := levenshtein.ComputeDistance(a, b)
	n := utf8.RuneCountInString(a)
	if m := utf8.RuneCountInString(b); m > n {
		n = m
	}
	return 1.0 - float64(d)/float64(n)
}

// jaccard returns |A∩B| / |A∪B| on two sets.
func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if _, ok := b[k]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

var stopwords = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "to": {}, "for": {}, "of": {}, "in": {},
	"on": {}, "at": {}, "and": {}, "or": {}, "with": {}, "by": {}, "as": {},
	"is": {}, "be": {}, "use": {},
}

// tokenSet splits a title into content tokens: lowercase, no stopwords, len≥3.
func tokenSet(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || (!unicode.IsLetter(r) && !unicode.IsDigit(r))
	}) {
		t := strings.ToLower(tok)
		if len(t) < 3 {
			continue
		}
		if _, drop := stopwords[t]; drop {
			continue
		}
		out[t] = struct{}{}
	}
	return out
}

// phaseEq treats empty phase and any other empty-phase as matching — so that
// plans without headers still correlate.
func phaseEq(a, b string) bool { return a == b }

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
