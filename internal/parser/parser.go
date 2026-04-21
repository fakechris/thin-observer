// Package parser extracts phases and checklist items from plan/todo/progress
// markdown files produced by coding agents. It is deliberately tolerant:
// file names are not bound, frontmatter is optional, and phase status is
// inferred from common patterns.
package parser

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Well-known plan-family file names. Observer also accepts anything matching
// the content shape, so this is a hint layer, not a filter.
var PlanFileHints = []string{
	"task_plan.md", "plan.md", "todo.md", "tasks.md", "requirements.md",
	"findings.md", "notes.md", "research.md",
	"progress.md", "session.md", "log.md",
}

var (
	// "## Phase 1: Research [done]" or "## Phase 1 [in_progress]" or "## Research"
	phaseHeaderRe = regexp.MustCompile(`^(#{1,3})\s+(.*?)(?:\s*\[(.+?)\])?\s*$`)
	// "- [x] foo", "- [ ] foo", "- [X] foo", "- [~] foo", "* [ ] foo"
	checklistRe = regexp.MustCompile(`^\s*[-*+]\s+\[([ xX~/\-])\]\s+(.*)$`)
	// Optional task ref like "[T-07]" somewhere in the title (PRD-compat, but not required)
	taskRefRe = regexp.MustCompile(`\[T-(\d+)\]`)
	// YAML frontmatter delimiters
	frontmatterDelim = "---"
)

// ParseFile reads a markdown file and returns a PlanDoc.
func ParseFile(path string) (*PlanDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	doc := Parse(string(data))
	doc.SourceFile = path
	doc.ParsedAt = time.Now().UTC()
	h := sha256.Sum256(data)
	doc.RawHash = hex.EncodeToString(h[:])
	return doc, nil
}

// Parse parses the given markdown content into a PlanDoc.
func Parse(content string) *PlanDoc {
	doc := &PlanDoc{ParsedAt: time.Now().UTC()}

	lines := splitLines(content)
	idx := 0
	idx = parseFrontmatter(lines, idx, &doc.Frontmatter)

	var current *Phase
	for ; idx < len(lines); idx++ {
		line := lines[idx]
		lineNum := idx + 1 // 1-based

		// Phase header?
		if m := phaseHeaderRe.FindStringSubmatch(line); m != nil && isPhaseLike(m[2], m[1]) {
			// Start a new phase. Close previous phase (append already handled below).
			current = &Phase{
				Name:   strings.TrimSpace(stripTaskRef(m[2])),
				Status: normalizePhaseStatus(m[3]),
				Line:   lineNum,
			}
			doc.Phases = append(doc.Phases, *current)
			// Keep pointer into slice — append may reallocate, so re-acquire:
			current = &doc.Phases[len(doc.Phases)-1]
			continue
		}

		// Checklist item?
		if m := checklistRe.FindStringSubmatch(line); m != nil {
			item := TaskItem{
				Title:  strings.TrimSpace(stripTaskRef(m[2])),
				Status: checklistStatus(m[1]),
				Line:   lineNum,
			}
			if refM := taskRefRe.FindStringSubmatch(m[2]); refM != nil {
				item.Ref = "T-" + refM[1]
			}
			if current == nil {
				// Synthesize an anonymous phase to hold orphan tasks.
				doc.Phases = append(doc.Phases, Phase{Name: "(ungrouped)", Status: "unknown", Line: lineNum})
				current = &doc.Phases[len(doc.Phases)-1]
			}
			current.Tasks = append(current.Tasks, item)
			continue
		}
	}

	// Infer phase status from task rollup when unspecified.
	for i := range doc.Phases {
		ph := &doc.Phases[i]
		if ph.Status == "" || ph.Status == "unknown" {
			ph.Status = rollupPhaseStatus(ph)
		}
	}

	return doc
}

func isPhaseLike(title, hashes string) bool {
	// Accept h1/h2/h3 that look like sections (not title of the whole document).
	// We treat any heading level 1-3 as a phase candidate. The very first h1 often
	// is the document title and won't have task children — that's fine, it will
	// just produce an empty phase.
	_ = hashes
	t := strings.TrimSpace(title)
	return t != ""
}

func checklistStatus(marker string) string {
	switch marker {
	case "x", "X":
		return "done"
	case "~":
		return "skipped"
	case "/", "-":
		return "in_progress"
	case " ":
		return "pending"
	default:
		return "unknown"
	}
}

func normalizePhaseStatus(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "done", "complete", "completed", "finished":
		return "done"
	case "in_progress", "in-progress", "running", "active", "doing":
		return "in_progress"
	case "pending", "todo", "not_started", "not-started":
		return "pending"
	case "":
		return ""
	default:
		return "unknown"
	}
}

func rollupPhaseStatus(ph *Phase) string {
	if len(ph.Tasks) == 0 {
		return "pending"
	}
	var done, total, inProgress int
	for _, t := range ph.Tasks {
		total++
		if t.Status == "done" || t.Status == "skipped" {
			done++
		}
		if t.Status == "in_progress" {
			inProgress++
		}
	}
	if done == total {
		return "done"
	}
	if inProgress > 0 || done > 0 {
		return "in_progress"
	}
	return "pending"
}

func stripTaskRef(s string) string {
	return taskRefRe.ReplaceAllString(s, "")
}

func parseFrontmatter(lines []string, start int, fm *Frontmatter) int {
	if fm.Raw == nil {
		fm.Raw = map[string]string{}
	}
	if start >= len(lines) || strings.TrimSpace(lines[start]) != frontmatterDelim {
		return start
	}
	i := start + 1
	for ; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == frontmatterDelim {
			i++
			break
		}
		if idx := strings.Index(line, ":"); idx > 0 {
			k := strings.TrimSpace(line[:idx])
			v := strings.TrimSpace(line[idx+1:])
			v = strings.Trim(v, `"'`)
			fm.Raw[k] = v
			switch k {
			case "objective_id":
				fm.ObjectiveID = v
			case "project_id":
				fm.ProjectID = v
			case "plan_id":
				fm.PlanID = v
			case "plan_rev":
				if n, err := strconv.Atoi(v); err == nil {
					fm.PlanRev = n
				}
			case "title":
				fm.Title = v
			}
		}
	}
	return i
}

func splitLines(s string) []string {
	// Handle \r\n and \n uniformly.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	scanner := bufio.NewScanner(strings.NewReader(s))
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	var out []string
	for scanner.Scan() {
		out = append(out, scanner.Text())
	}
	return out
}

// LooksLikePlanFile returns true if the file name suggests a plan-family doc.
// Content-based detection is also possible but is left to the watcher.
func LooksLikePlanFile(name string) bool {
	lname := strings.ToLower(name)
	for _, h := range PlanFileHints {
		if lname == h {
			return true
		}
	}
	return false
}
