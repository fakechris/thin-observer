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
	// PhaseChangePenalty is subtracted from confidence when two candidates
	// sit in different phases but are otherwise a good match. Keeps us
	// tolerant of agents reorganizing sections ("Pending" → "In Progress")
	// without losing task identity. Default 0.15.
	PhaseChangePenalty float64
}

// Heuristic implements Inferrer with Levenshtein + token-overlap heuristics.
type Heuristic struct{ Options }

// New returns a Heuristic with sensible defaults.
func New() *Heuristic {
	return &Heuristic{Options{
		RenameThreshold:    0.80,
		SplitOverlap:       0.40,
		MergeOverlap:       0.40,
		PhaseChangePenalty: 0.15,
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
	if o.PhaseChangePenalty > 0 {
		h.PhaseChangePenalty = o.PhaseChangePenalty
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

	// -- Pass 1: exact title match. Same phase → confidence 1.0; if nothing
	// same-phase matches, fall back to cross-phase with a penalty so a task
	// reorganized from "Pending" to "In Progress" (common agent behavior)
	// doesn't get orphaned into NewTask.
	for ni, n := range newItems {
		if usedNew[ni] {
			continue
		}
		matched := -1
		conf := 1.0
		for oi, o := range old {
			if usedOld[oi] {
				continue
			}
			if o.Title == n.Title && phaseEq(o.Phase, n.Phase) {
				matched = oi
				break
			}
		}
		if matched < 0 {
			for oi, o := range old {
				if usedOld[oi] {
					continue
				}
				if o.Title == n.Title {
					matched = oi
					conf = 1.0 - h.PhaseChangePenalty
					break
				}
			}
		}
		if matched >= 0 {
			decisions = append(decisions, Decision{
				Kind: DecisionSameTask, OldID: old[matched].ID, NewIndex: ni, Confidence: conf,
			})
			usedOld[matched] = true
			usedNew[ni] = true
		}
	}

	// -- Pass 2: alias match (new.title ∈ old.aliases). Cross-phase allowed
	// with a penalty — agents routinely move tasks between "Pending" / "In
	// Progress" / "Done" sections without changing the task itself.
	for ni, n := range newItems {
		if usedNew[ni] {
			continue
		}
		for oi, o := range old {
			if usedOld[oi] {
				continue
			}
			if containsStr(o.Aliases, n.Title) {
				conf := 0.95
				if !phaseEq(o.Phase, n.Phase) {
					conf -= h.PhaseChangePenalty
				}
				decisions = append(decisions, Decision{
					Kind: DecisionSameTask, OldID: o.ID, NewIndex: ni, Confidence: conf,
				})
				usedOld[oi] = true
				usedNew[ni] = true
				break
			}
		}
	}

	// -- Pass 3: rename — Levenshtein ratio ≥ threshold. Cross-phase allowed
	// with a penalty; rank candidates by final confidence so same-phase wins
	// ties over cross-phase.
	//
	// We also boost short-title matches when token overlap is strong: three
	// edits on a 10-char string crater the raw ratio, but "Add API" →
	// "Add the API" is clearly a rename to a human.
	type cand struct {
		oi, ni int
		conf   float64
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
			r := ratio(o.Title, n.Title)
			if r >= 1.0 {
				continue // identical — pass 1 should have caught it
			}
			// Short-title boost: if the raw ratio is borderline but both titles
			// produce the exact same content-token set, treat as rename at
			// threshold. Catches "Add API" → "Add the API" where short
			// lengths make Levenshtein pessimistic. Equality (not subset) is
			// required so we don't steal work from the split pass, where a
			// child's tokens are a strict subset of the umbrella's.
			if r < h.RenameThreshold &&
				len(oldTokens[oi]) > 0 &&
				tokensEqual(oldTokens[oi], newTokens[ni]) {
				r = h.RenameThreshold
			}
			if r < h.RenameThreshold {
				continue
			}
			conf := r
			if !phaseEq(o.Phase, n.Phase) {
				conf -= h.PhaseChangePenalty
				if conf < 0.5 {
					continue // penalty made it too weak — let split/merge try
				}
			}
			cands = append(cands, cand{oi, ni, conf})
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].conf > cands[j].conf })
	for _, c := range cands {
		if usedOld[c.oi] || usedNew[c.ni] {
			continue
		}
		decisions = append(decisions, Decision{
			Kind: DecisionRename, OldID: old[c.oi].ID, NewIndex: c.ni, Confidence: c.conf,
		})
		usedOld[c.oi] = true
		usedNew[c.ni] = true
	}

	// -- Pass 4: split (1 unused old → 2+ unused new, token overlap). Collect
	// every candidate parent's best split first, then commit them in order of
	// confidence so the strongest splits win against overlapping greedy ones.
	type splitCand struct {
		oi       int
		children []int
		conf     float64
	}
	var splits []splitCand
	for oi, o := range old {
		if usedOld[oi] {
			continue
		}
		if len(oldTokens[oi]) == 0 {
			continue
		}
		var children []int
		var overlapSum float64
		var crossPhase int
		for ni, n := range newItems {
			if usedNew[ni] {
				continue
			}
			if len(newTokens[ni]) == 0 {
				continue
			}
			ov := jaccard(oldTokens[oi], newTokens[ni])
			if ov < h.SplitOverlap {
				continue
			}
			// Child should be roughly at the umbrella's scale or smaller.
			if utf8.RuneCountInString(n.Title) >= utf8.RuneCountInString(o.Title)+20 {
				continue
			}
			children = append(children, ni)
			overlapSum += ov
			if !phaseEq(o.Phase, n.Phase) {
				crossPhase++
			}
		}
		if len(children) < 2 {
			continue
		}
		conf := overlapSum / float64(len(children))
		if crossPhase > 0 {
			conf -= h.PhaseChangePenalty * float64(crossPhase) / float64(len(children))
		}
		if conf > 0.9 {
			conf = 0.9
		}
		if conf < 0.3 {
			continue
		}
		splits = append(splits, splitCand{oi: oi, children: children, conf: conf})
	}
	sort.Slice(splits, func(i, j int) bool { return splits[i].conf > splits[j].conf })
	for _, s := range splits {
		if usedOld[s.oi] {
			continue
		}
		available := s.children[:0]
		for _, ci := range s.children {
			if !usedNew[ci] {
				available = append(available, ci)
			}
		}
		if len(available) < 2 {
			continue
		}
		decisions = append(decisions, Decision{
			Kind: DecisionSplit, OldID: old[s.oi].ID,
			NewIndex: available[0], ChildNewIndexes: append([]int(nil), available...),
			Confidence: s.conf,
		})
		usedOld[s.oi] = true
		for _, ci := range available {
			usedNew[ci] = true
		}
	}

	// -- Pass 5: merge (2+ unused old → 1 unused new, token overlap). Same
	// pattern as split: collect, sort, commit.
	type mergeCand struct {
		ni      int
		parents []int
		conf    float64
	}
	var merges []mergeCand
	for ni, n := range newItems {
		if usedNew[ni] {
			continue
		}
		if len(newTokens[ni]) == 0 {
			continue
		}
		var parents []int
		var overlapSum float64
		var crossPhase int
		for oi, o := range old {
			if usedOld[oi] {
				continue
			}
			if len(oldTokens[oi]) == 0 {
				continue
			}
			ov := jaccard(oldTokens[oi], newTokens[ni])
			if ov < h.MergeOverlap {
				continue
			}
			if utf8.RuneCountInString(o.Title) >= utf8.RuneCountInString(n.Title)+20 {
				continue
			}
			parents = append(parents, oi)
			overlapSum += ov
			if !phaseEq(o.Phase, n.Phase) {
				crossPhase++
			}
		}
		if len(parents) < 2 {
			continue
		}
		conf := overlapSum / float64(len(parents))
		if crossPhase > 0 {
			conf -= h.PhaseChangePenalty * float64(crossPhase) / float64(len(parents))
		}
		if conf > 0.9 {
			conf = 0.9
		}
		if conf < 0.3 {
			continue
		}
		merges = append(merges, mergeCand{ni: ni, parents: parents, conf: conf})
	}
	sort.Slice(merges, func(i, j int) bool { return merges[i].conf > merges[j].conf })
	for _, m := range merges {
		if usedNew[m.ni] {
			continue
		}
		available := m.parents[:0]
		for _, oi := range m.parents {
			if !usedOld[oi] {
				available = append(available, oi)
			}
		}
		if len(available) < 2 {
			continue
		}
		oldIDs := make([]string, len(available))
		for i, oi := range available {
			oldIDs[i] = old[oi].ID
		}
		decisions = append(decisions, Decision{
			Kind: DecisionMerge, OldIDs: oldIDs, NewIndex: m.ni, Confidence: m.conf,
		})
		usedNew[m.ni] = true
		for _, oi := range available {
			usedOld[oi] = true
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

// tokenSet splits a title into content tokens. Rules:
//   - lowercase for comparison
//   - drop stopwords (a/the/for/to/...)
//   - drop tokens < 3 runes EXCEPT when the original form was all-uppercase
//     (acronyms: CI, DB, UI, PR, QA) or contained a digit (S3, v2, i18n).
//     These short tokens carry most of the signal in engineering titles.
func tokenSet(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || (!unicode.IsLetter(r) && !unicode.IsDigit(r))
	}) {
		lower := strings.ToLower(tok)
		if _, drop := stopwords[lower]; drop {
			continue
		}
		if utf8.RuneCountInString(lower) < 3 {
			if !isAcronymOrCode(tok) {
				continue
			}
		}
		out[lower] = struct{}{}
	}
	return out
}

// isAcronymOrCode reports whether a short token carries enough engineering
// signal to be worth keeping: either entirely uppercase ("CI", "DB") or
// containing at least one digit ("S3", "v2").
func isAcronymOrCode(tok string) bool {
	hasLetter := false
	allUpper := true
	hasDigit := false
	for _, r := range tok {
		if unicode.IsDigit(r) {
			hasDigit = true
		}
		if unicode.IsLetter(r) {
			hasLetter = true
			if !unicode.IsUpper(r) {
				allUpper = false
			}
		}
	}
	if hasDigit {
		return true
	}
	return hasLetter && allUpper
}

// tokensSubset reports whether every token in a is also in b. Returns false
// if a is empty — an empty "subset" isn't informative.
func tokensSubset(a, b map[string]struct{}) bool {
	if len(a) == 0 {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

// tokensEqual reports whether a and b contain exactly the same tokens.
func tokensEqual(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
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
