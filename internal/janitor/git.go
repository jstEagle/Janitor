package janitor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func trim(b []byte) string { return strings.TrimSpace(string(b)) }
func git(path string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	all := append([]string{"--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-C", path}, args...)
	cmd := exec.CommandContext(ctx, "git", all...)
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "GIT_") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0")
	out, e := cmd.Output()
	if e != nil {
		return nil, fmt.Errorf("git %s in %s failed: %w", strings.Join(args, " "), path, e)
	}
	return out, nil
}

type GitInfo struct {
	Head        string `json:"head"`
	Branch      string `json:"branch"`
	Main        bool   `json:"main"`
	Locked      bool   `json:"locked"`
	Dirty       bool   `json:"dirty"`
	Ignored     bool   `json:"ignored"`
	Recoverable bool   `json:"recoverable"`
	Reason      string `json:"reason"`
	CommonDir   string `json:"common_dir"`
}

func worktreePaths(p string) ([]string, error) {
	b, e := git(p, "worktree", "list", "--porcelain", "-z")
	if e != nil {
		return nil, e
	}
	var paths []string
	for _, v := range strings.Split(string(b), "\x00") {
		if strings.HasPrefix(v, "worktree ") {
			paths = append(paths, strings.TrimPrefix(v, "worktree "))
		}
	}
	return paths, nil
}
func inspectGit(p string) (GitInfo, error) {
	g := GitInfo{}
	b, e := git(p, "rev-parse", "--verify", "HEAD")
	if e != nil {
		return g, e
	}
	g.Head = trim(b)
	b, e = git(p, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if e != nil {
		return g, e
	}
	g.CommonDir = trim(b)
	b, e = git(p, "worktree", "list", "--porcelain", "-z")
	if e != nil {
		return g, e
	}
	current := ""
	first := true
	for _, v := range strings.Split(string(b), "\x00") {
		if strings.HasPrefix(v, "worktree ") {
			current = strings.TrimPrefix(v, "worktree ")
			if first {
				g.Main = current == p
				first = false
			}
		}
		if current == p {
			if v == "locked" || strings.HasPrefix(v, "locked ") {
				g.Locked = true
			}
			if strings.HasPrefix(v, "branch ") {
				g.Branch = strings.TrimPrefix(v, "branch ")
			}
		}
	}
	b, e = git(p, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=matching")
	if e != nil {
		return g, e
	}
	for _, record := range strings.Split(string(b), "\x00") {
		if record == "" {
			continue
		}
		if strings.HasPrefix(record, "!! ") {
			g.Ignored = true
		} else {
			g.Dirty = true
		}
	}
	if g.Branch == "" {
		g.Reason = "detached HEAD: preserve with a branch before retirement"
		return g, nil
	}
	tip, e := git(p, "rev-parse", "--verify", g.Branch)
	if e != nil || trim(tip) != g.Head {
		g.Reason = "branch no longer preserves HEAD"
		return g, nil
	}
	b, e = git(p, "for-each-ref", "--format=%(refname)", "--contains", g.Head, "refs/remotes/")
	if e != nil {
		return g, e
	}
	g.Recoverable = trim(b) != ""
	if !g.Recoverable {
		g.Reason = "HEAD is not reachable from a remote-tracking ref (no network fetch performed)"
	}
	if _, e = os.Lstat(filepath.Join(p, ".gitmodules")); e == nil {
		g.Recoverable = false
		g.Reason = "submodules require owner-managed retirement"
	}
	return g, nil
}
func artifactTracked(workspace, rel string) (bool, error) {
	b, e := git(workspace, "--literal-pathspecs", "ls-files", "-z", "--", rel)
	return len(b) > 0, e
}
