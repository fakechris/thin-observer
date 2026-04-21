package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chris/thin-observer/internal/ingest"
	"github.com/chris/thin-observer/internal/lineage"
	"github.com/chris/thin-observer/internal/parser"
	"github.com/chris/thin-observer/internal/store"
)

func testServer(t *testing.T) (*Server, *store.Store, store.Worktree) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	_ = s.UpsertProject(ctx, store.Project{ID: "p1", Name: "demo", RootPath: "/tmp/demo"})
	w := store.Worktree{ID: "w1", ProjectID: "p1", Name: "feature", Path: "/tmp/demo/feature"}
	_ = s.UpsertWorktree(ctx, w)

	in := ingest.New(s)
	in.Inferrer = lineage.New()
	doc, _ := parser.ParseFile(filepath.Join("..", "..", "testdata", "plans", "planning-with-files.md"))
	_, _ = in.Apply(ctx, w, doc)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := New(s, logger)
	if err != nil {
		t.Fatal(err)
	}
	return srv, s, w
}

func TestKanbanRenders(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	body := w.Body.String()
	for _, want := range []string{"Inbox", "Active", "Done", "thin-observer", "feature"} {
		if !strings.Contains(body, want) {
			t.Errorf("kanban missing %q", want)
		}
	}
}

func TestTaskPageAndOverride(t *testing.T) {
	srv, s, w := testServer(t)
	tasks, _ := s.TasksByWorktree(context.Background(), w.ID)
	if len(tasks) == 0 {
		t.Fatal("expected seeded tasks")
	}
	taskID := tasks[0].ID

	r := httptest.NewRequest("GET", "/task/"+taskID, nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Fatalf("task detail status=%d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, tasks[0].CurrentTitle) {
		t.Errorf("task page missing title: %q", tasks[0].CurrentTitle)
	}
	if !strings.Contains(body, "Manual override") {
		t.Errorf("task page missing override form")
	}

	// POST an override; expect redirect back to /task/<id>.
	form := url.Values{}
	form.Set("kind", "mark_dropped")
	form.Set("note", "we will not do this one")
	r2 := httptest.NewRequest("POST", "/override/"+taskID, strings.NewReader(form.Encode()))
	r2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r2.Header.Set("Origin", "http://"+r2.Host)
	rec2 := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec2, r2)
	if rec2.Code != http.StatusSeeOther {
		t.Fatalf("override status=%d want 303", rec2.Code)
	}
	overs, _ := s.OverridesForTask(context.Background(), taskID)
	if len(overs) != 1 || overs[0].Kind != "mark_dropped" {
		t.Fatalf("expected 1 override kind=mark_dropped, got %+v", overs)
	}
	// mark_dropped must actually flip the task's status, not just log.
	t2, err := s.TaskByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if t2.Status != "dropped" {
		t.Errorf("task status=%q want dropped — override did not apply", t2.Status)
	}
}

func TestOverrideRejectsCrossOrigin(t *testing.T) {
	srv, s, w := testServer(t)
	tasks, _ := s.TasksByWorktree(context.Background(), w.ID)
	if len(tasks) == 0 {
		t.Fatal("expected seeded tasks")
	}
	taskID := tasks[0].ID
	form := url.Values{}
	form.Set("kind", "mark_dropped")
	r := httptest.NewRequest("POST", "/override/"+taskID, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://attacker.example")
	r.RemoteAddr = "203.0.113.9:1234" // non-loopback
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin override status=%d want 403", rec.Code)
	}
	overs, _ := s.OverridesForTask(context.Background(), taskID)
	if len(overs) != 0 {
		t.Errorf("cross-origin POST still wrote %d overrides", len(overs))
	}
}

func TestOverrideMarkSameSetsSupersedes(t *testing.T) {
	srv, s, w := testServer(t)
	ctx := context.Background()
	tasks, _ := s.TasksByWorktree(ctx, w.ID)
	if len(tasks) < 2 {
		t.Fatal("need at least two tasks")
	}
	dupID, canonID := tasks[0].ID, tasks[1].ID

	form := url.Values{}
	form.Set("kind", "mark_same")
	form.Set("related_task_id", canonID)
	r := httptest.NewRequest("POST", "/override/"+dupID, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://"+r.Host)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("override status=%d want 303", rec.Code)
	}
	got, err := s.TaskByID(ctx, dupID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Supersedes != canonID {
		t.Errorf("supersedes=%q want %q", got.Supersedes, canonID)
	}
	if got.Status != "dropped" {
		t.Errorf("status=%q want dropped", got.Status)
	}
}

func TestArchivePage(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest("GET", "/archive", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Fatalf("archive status=%d body=%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "Archive") {
		t.Error("archive page missing heading")
	}
}

func TestStaticCSSServed(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest("GET", "/static/style.css", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Fatalf("css status=%d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/css" {
		t.Errorf("wrong content-type: %q", ct)
	}
}

func TestHealthz(t *testing.T) {
	srv, _, _ := testServer(t)
	r := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	if rec.Code != 200 || rec.Body.String() != "ok" {
		t.Errorf("healthz failed: code=%d body=%s", rec.Code, rec.Body)
	}
}

func TestBucketizeClassification(t *testing.T) {
	now := time.Now()
	cards := []card{
		{Task: store.Task{Status: "in_progress", LastSeenAt: now, Confidence: 1.0}},
		{Task: store.Task{Status: "done", LastSeenAt: now, Confidence: 1.0}},
		{Task: store.Task{Status: "lost", LastSeenAt: now, Confidence: 1.0}},
		{Task: store.Task{Status: "pending", LastSeenAt: now.Add(-2 * time.Hour), Confidence: 1.0}},
		{Task: store.Task{Status: "pending", LastSeenAt: now, Confidence: 1.0}},
		{Task: store.Task{Status: "pending", LastSeenAt: now, Confidence: 0.5}},
	}
	cols := bucketize(cards, now)
	counts := map[string]int{}
	for _, c := range cols {
		counts[c.Key] = len(c.Cards)
	}
	if counts["active"] != 1 {
		t.Errorf("active=%d want 1", counts["active"])
	}
	if counts["done"] != 1 {
		t.Errorf("done=%d want 1", counts["done"])
	}
	if counts["attention"] != 2 { // lost + low-confidence
		t.Errorf("attention=%d want 2", counts["attention"])
	}
	if counts["stalled"] != 1 {
		t.Errorf("stalled=%d want 1", counts["stalled"])
	}
	if counts["inbox"] != 1 {
		t.Errorf("inbox=%d want 1", counts["inbox"])
	}
}
