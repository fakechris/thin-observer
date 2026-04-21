// Package discovery finds worktrees to observe.
//
// Two modes:
//   1. Config-file registration: user explicitly lists repos to watch.
//   2. Auto-scan: walk a root directory (default: ~/workspace) looking for
//      git repos. Git worktrees (listed via `git worktree list`) are included.
package discovery

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is loaded from ~/.config/thin-observer/config.yaml.
type Config struct {
	// Roots are directories to auto-scan for git repos.
	Roots []string `yaml:"roots"`
	// Projects are explicit project registrations (bypasses scan).
	Projects []ProjectConfig `yaml:"projects"`
	// PlanFiles is the set of additional file names to treat as plan docs.
	PlanFiles []string `yaml:"plan_files"`
	// Exclude matches path substrings to skip when scanning.
	Exclude []string `yaml:"exclude"`
}

type ProjectConfig struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

// SaveConfig writes the config to the given path, creating parent directories
// as needed.
func SaveConfig(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// AddProject appends a project to the config if not already present.
// It returns true if the project was added, false if it was already registered.
func AddProject(cfg *Config, name, absPath string) bool {
	for _, p := range cfg.Projects {
		expanded, _ := expand(p.Path)
		if expanded == absPath {
			return false
		}
	}
	cfg.Projects = append(cfg.Projects, ProjectConfig{Name: name, Path: absPath})
	return true
}

// RemoveProject removes a project matching name or path from the config.
// It returns true if a project was removed.
func RemoveProject(cfg *Config, key string) bool {
	for i, p := range cfg.Projects {
		expanded, _ := expand(p.Path)
		if p.Name == key || p.Path == key || expanded == key || filepath.Base(expanded) == key {
			cfg.Projects = append(cfg.Projects[:i], cfg.Projects[i+1:]...)
			return true
		}
	}
	return false
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return &Config{}, nil
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Found describes a discovered worktree.
type Found struct {
	ProjectRoot string // git repo root (or main worktree root)
	ProjectName string // base name of ProjectRoot
	WorktreePath string
	WorktreeName string // base name of WorktreePath; for the main worktree, equals project name
}

// Discover walks the configured roots and known projects, returning all
// worktrees thin-observer should monitor.
func Discover(cfg *Config) ([]Found, error) {
	seen := map[string]struct{}{}
	var out []Found

	add := func(f Found) {
		if _, ok := seen[f.WorktreePath]; ok {
			return
		}
		seen[f.WorktreePath] = struct{}{}
		out = append(out, f)
	}

	// Explicit projects.
	for _, p := range cfg.Projects {
		expanded, _ := expand(p.Path)
		if !isGitRepo(expanded) {
			continue
		}
		mainRoot := expanded
		name := p.Name
		if name == "" {
			name = filepath.Base(mainRoot)
		}
		add(Found{
			ProjectRoot:  mainRoot,
			ProjectName:  name,
			WorktreePath: mainRoot,
			WorktreeName: filepath.Base(mainRoot),
		})
		for _, wt := range listWorktrees(mainRoot) {
			add(Found{
				ProjectRoot:  mainRoot,
				ProjectName:  name,
				WorktreePath: wt,
				WorktreeName: filepath.Base(wt),
			})
		}
	}

	// Scan roots.
	for _, r := range cfg.Roots {
		expanded, _ := expand(r)
		walkRoots(expanded, cfg.Exclude, func(repoRoot string) {
			name := filepath.Base(repoRoot)
			add(Found{
				ProjectRoot:  repoRoot,
				ProjectName:  name,
				WorktreePath: repoRoot,
				WorktreeName: name,
			})
			for _, wt := range listWorktrees(repoRoot) {
				add(Found{
					ProjectRoot:  repoRoot,
					ProjectName:  name,
					WorktreePath: wt,
					WorktreeName: filepath.Base(wt),
				})
			}
		})
	}

	return out, nil
}

func expand(path string) (string, error) {
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path, err
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~")), nil
	}
	return path, nil
}

// isGitRepo checks whether path/.git exists (file for worktrees, dir for main).
func isGitRepo(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil
}

// walkRoots walks a root directory looking for git repos. Does not recurse
// into found repos (submodules intentionally ignored for now).
func walkRoots(root string, exclude []string, fn func(repoRoot string)) {
	if root == "" {
		return
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return
	}
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		// Never descend into .git / node_modules / similar noise — these are
		// not git repos themselves and walking them wastes a lot of syscalls.
		base := filepath.Base(p)
		if base == ".git" || base == "node_modules" || base == ".venv" ||
			base == "__pycache__" {
			return filepath.SkipDir
		}
		for _, ex := range exclude {
			if ex != "" && strings.Contains(p, ex) {
				return filepath.SkipDir
			}
		}
		if isGitRepo(p) {
			fn(p)
			return filepath.SkipDir
		}
		return nil
	})
}

// listWorktrees shells out to `git worktree list` to find auxiliary worktrees.
// Errors are ignored — discovery should degrade gracefully.
func listWorktrees(repoRoot string) []string {
	cmd := exec.Command("git", "-C", repoRoot, "worktree", "list", "--porcelain")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil
	}
	if err := cmd.Start(); err != nil {
		return nil
	}
	defer cmd.Wait()
	// Normalize repoRoot via EvalSymlinks so macOS /tmp → /private/var/... matches.
	canonicalRoot, _ := filepath.EvalSymlinks(repoRoot)
	if canonicalRoot == "" {
		canonicalRoot = repoRoot
	}
	var out []string
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "worktree ") {
			path := strings.TrimPrefix(line, "worktree ")
			if path != repoRoot && path != canonicalRoot {
				out = append(out, path)
			}
		}
	}
	return out
}
