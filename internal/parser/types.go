package parser

import "time"

type PlanDoc struct {
	SourceFile  string    `json:"source_file"`
	ParsedAt    time.Time `json:"parsed_at"`
	Frontmatter Frontmatter `json:"frontmatter"`
	Phases      []Phase   `json:"phases"`
	RawHash     string    `json:"raw_hash"`
}

type Frontmatter struct {
	ObjectiveID string `json:"objective_id,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	PlanID      string `json:"plan_id,omitempty"`
	PlanRev     int    `json:"plan_rev,omitempty"`
	Title       string `json:"title,omitempty"`
	Raw         map[string]string `json:"raw,omitempty"`
}

type Phase struct {
	Name   string     `json:"name"`
	Status string     `json:"status"` // pending | in_progress | done | unknown
	Line   int        `json:"line"`
	Tasks  []TaskItem `json:"tasks"`
}

type TaskItem struct {
	Title  string `json:"title"`
	Status string `json:"status"` // pending | in_progress | done | skipped | unknown
	Line   int    `json:"line"`
	Ref    string `json:"ref,omitempty"` // T-07 if present, optional — not required
}

// Stats summarizes a plan document at a glance.
type Stats struct {
	TotalTasks     int
	DoneTasks      int
	InProgressTasks int
	PendingTasks   int
	Phases         int
	DonePhases     int
}

func (p *PlanDoc) Stats() Stats {
	var s Stats
	s.Phases = len(p.Phases)
	for _, ph := range p.Phases {
		if ph.Status == "done" {
			s.DonePhases++
		}
		for _, t := range ph.Tasks {
			s.TotalTasks++
			switch t.Status {
			case "done":
				s.DoneTasks++
			case "in_progress":
				s.InProgressTasks++
			case "pending":
				s.PendingTasks++
			}
		}
	}
	return s
}
