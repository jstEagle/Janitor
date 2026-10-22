package janitor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fixture struct {
	a          *App
	root, repo string
	now        time.Time
}

func command(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	b, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %s %v", args, b, e)
	}
	return strings.TrimSpace(string(b))
}
func write(t *testing.T, path, body string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
}
func setup(t *testing.T) *fixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "projects")
	repo := filepath.Join(root, "app")
	if e := os.MkdirAll(repo, 0700); e != nil {
		t.Fatal(e)
	}
	command(t, repo, "init", "-b", "main")
	write(t, filepath.Join(repo, "package.json"), "{}\n")
	write(t, filepath.Join(repo, ".gitignore"), "node_modules/\n.next/\n.env\n")
	command(t, repo, "add", ".")
	command(t, repo, "commit", "-m", "initial")
	command(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	c := DefaultConfig()
	c.Roots = []string{root}
	c.ArtifactIdleHours = 1
	c.WorktreeIdleHours = 1
	c.RetireWorktrees = true
	a, e := New(c, filepath.Join(base, "state"))
	if e != nil {
		t.Fatal(e)
	}
	f := &fixture{a: a, root: root, repo: repo, now: time.Now().Add(time.Hour)}
	a.Now = func() time.Time { return f.now }
	return f
}
func (f *fixture) age(t *testing.T) {
	t.Helper()
	if _, e := f.a.Scan(); e != nil {
		t.Fatal(e)
	}
	f.now = f.now.Add(2 * time.Hour)
}
func (f *fixture) artifact(t *testing.T, workspace string) string {
	p := filepath.Join(workspace, "node_modules")
	write(t, filepath.Join(p, "a", "index.js"), strings.Repeat("a", 8192))
	return p
}
func (f *fixture) enroll(t *testing.T, path string, retire bool) {
	t.Helper()
	if e := f.a.Enroll(path, []string{"node_modules", ".next"}, retire); e != nil {
		t.Fatal(e)
	}
}
func (f *fixture) plan(t *testing.T) Plan {
	t.Helper()
	p, e := f.a.Plan()
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func exists(t *testing.T, path string, want bool) {
	t.Helper()
	_, e := os.Lstat(path)
	if want && e != nil {
		t.Fatalf("expected %s: %v", path, e)
	}
	if !want && !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("expected missing %s: %v", path, e)
	}
}
func (f *fixture) worktree(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(f.root, name)
	command(t, f.repo, "worktree", "add", "-b", name, p)
	return p
}

func TestArtifactPlanApplyAndReplay(t *testing.T) {
	f := setup(t)
	p := f.artifact(t, f.repo)
	f.enroll(t, f.repo, false)
	f.age(t)
	plan := f.plan(t)
	if len(plan.Actions) != 1 {
		t.Fatalf("actions: %+v workspaces: %+v", plan.Actions, plan.Inventory.Workspaces)
	}
	r, e := f.a.Apply(plan.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Completed) != 1 || r.Status != "completed" {
		t.Fatalf("%+v", r)
	}
	exists(t, p, false)
	exists(t, filepath.Join(f.repo, "package.json"), true)
	if _, e = f.a.Apply(plan.ID, false); e == nil {
		t.Fatal("replayed plan")
	}
	history, e := f.a.History()
	if e != nil || len(history) != 1 {
		t.Fatalf("history: %v %v", history, e)
	}
}
func TestUnenrolledAndFreshNeverEligible(t *testing.T) {
	f := setup(t)
	f.artifact(t, f.repo)
	f.age(t)
	if len(f.plan(t).Actions) != 0 {
		t.Fatal("unenrolled eligible")
	}
	f.enroll(t, f.repo, false)
	if len(f.plan(t).Actions) != 0 {
		t.Fatal("new observation eligible")
	}
}
func TestChangedContentInvalidatesEntirePlan(t *testing.T) {
	f := setup(t)
	p := f.artifact(t, f.repo)
	f.enroll(t, f.repo, false)
	f.age(t)
	plan := f.plan(t)
	write(t, filepath.Join(p, "new.js"), "new work")
	if _, e := f.a.Apply(plan.ID, false); e == nil {
		t.Fatal("changed plan applied")
	}
	exists(t, p, true)
}
func TestTrackedArtifactsProtected(t *testing.T) {
	f := setup(t)
	p := f.artifact(t, f.repo)
	command(t, f.repo, "add", "-f", filepath.Join(p, "a", "index.js"))
	command(t, f.repo, "commit", "-m", "vendor")
	f.enroll(t, f.repo, false)
	f.age(t)
	if len(f.plan(t).Actions) != 0 {
		t.Fatal("tracked data eligible")
	}
}
func TestLeaseAndPinProtection(t *testing.T) {
	f := setup(t)
	f.artifact(t, f.repo)
	f.enroll(t, f.repo, false)
	f.age(t)
	lease := Lease{id(), f.repo, os.Getpid(), f.now}
	if e := atomicJSON(filepath.Join(f.a.StateDir, "leases", lease.ID+".json"), lease); e != nil {
		t.Fatal(e)
	}
	if len(f.plan(t).Actions) != 0 {
		t.Fatal("lease ignored")
	}
	if e := f.a.ReleaseLease(lease.ID); e != nil {
		t.Fatal(e)
	}
	f.age(t)
	if e := f.a.Pin(f.repo, "working", false); e != nil {
		t.Fatal(e)
	}
	if len(f.plan(t).Actions) != 0 {
		t.Fatal("pin ignored")
	}
	if e := f.a.Pin(f.repo, "", true); e != nil {
		t.Fatal(e)
	}
	if len(f.plan(t).Actions) != 1 {
		t.Fatal("unpin failed")
	}
}
func TestGitWorktreeRetirementPreservesBranch(t *testing.T) {
	f := setup(t)
	wt := f.worktree(t, "task")
	f.enroll(t, wt, true)
	f.age(t)
	p := f.plan(t)
	if len(p.Actions) != 1 || p.Actions[0].Kind != "worktree" {
		t.Fatalf("%+v", p)
	}
	if _, e := f.a.Apply(p.ID, false); e != nil {
		t.Fatal(e)
	}
	exists(t, wt, false)
	command(t, f.repo, "rev-parse", "--verify", "refs/heads/task")
	exists(t, f.repo, true)
}
func TestWorktreeProtections(t *testing.T) {
	for _, scenario := range []string{"dirty", "untracked", "ignored", "detached", "unique", "locked", "submodule"} {
		t.Run(scenario, func(t *testing.T) {
			f := setup(t)
			wt := f.worktree(t, "task")
			switch scenario {
			case "dirty":
				write(t, filepath.Join(wt, "package.json"), "changed")
			case "untracked":
				write(t, filepath.Join(wt, "notes"), "important")
			case "ignored":
				write(t, filepath.Join(wt, ".env"), "private fixture")
			case "detached":
				command(t, wt, "checkout", "--detach")
			case "unique":
				write(t, filepath.Join(wt, "package.json"), "changed")
				command(t, wt, "commit", "-am", "unique")
			case "locked":
				command(t, f.repo, "worktree", "lock", wt)
			case "submodule":
				write(t, filepath.Join(wt, ".gitmodules"), "")
				command(t, wt, "add", ".")
				command(t, wt, "commit", "-m", "submodule marker")
				command(t, wt, "update-ref", "refs/remotes/origin/task", "HEAD")
			}
			f.enroll(t, wt, true)
			f.age(t)
			if len(f.plan(t).Actions) != 0 {
				t.Fatalf("%s eligible", scenario)
			}
			exists(t, wt, true)
		})
	}
}
func TestPrimaryNeverRetired(t *testing.T) {
	f := setup(t)
	f.enroll(t, f.repo, true)
	f.age(t)
	if len(f.plan(t).Actions) != 0 {
		t.Fatal("primary eligible")
	}
}
func TestSymlinksNeverFollowed(t *testing.T) {
	f := setup(t)
	outside := filepath.Join(filepath.Dir(f.root), "important")
	write(t, filepath.Join(outside, "secret"), "keep")
	p := f.artifact(t, f.repo)
	if e := os.Symlink(outside, filepath.Join(p, "link")); e != nil {
		t.Fatal(e)
	}
	f.enroll(t, f.repo, false)
	f.age(t)
	plan := f.plan(t)
	if _, e := f.a.Apply(plan.ID, false); e != nil {
		t.Fatal(e)
	}
	exists(t, filepath.Join(outside, "secret"), true)
}
func TestReplacedArtifactSymlinkRefused(t *testing.T) {
	f := setup(t)
	p := f.artifact(t, f.repo)
	f.enroll(t, f.repo, false)
	f.age(t)
	plan := f.plan(t)
	if e := os.Rename(p, p+"-saved"); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(p+"-saved", p); e != nil {
		t.Fatal(e)
	}
	if _, e := f.a.Apply(plan.ID, false); e == nil {
		t.Fatal("replacement accepted")
	}
	exists(t, p+"-saved", true)
}
func TestNestedRepositoryProtected(t *testing.T) {
	f := setup(t)
	p := f.artifact(t, f.repo)
	command(t, p, "init")
	f.enroll(t, f.repo, false)
	f.age(t)
	if len(f.plan(t).Actions) != 0 {
		t.Fatal("nested repository eligible")
	}
}
func TestHardlinkAccounting(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a"), strings.Repeat("x", 8192))
	if e := os.Link(filepath.Join(root, "a"), filepath.Join(root, "b")); e != nil {
		t.Fatal(e)
	}
	m := measure(root, nil, false)
	if m.Size.Logical != 8192 || m.Size.ReclaimEstimate != 0 || m.Size.Files != 1 {
		t.Fatalf("%+v", m.Size)
	}
}
func TestSharedGitStoreMeasured(t *testing.T) {
	f := setup(t)
	f.worktree(t, "task")
	i, e := f.a.Scan()
	if e != nil {
		t.Fatal(e)
	}
	if len(i.Repositories) != 1 || i.Repositories[0].Size.Logical == 0 {
		t.Fatalf("%+v", i.Repositories)
	}
}
func TestPlanExpiryPolicyAndInterruptedJournal(t *testing.T) {
	for _, scenario := range []string{"expired", "policy", "interrupted"} {
		t.Run(scenario, func(t *testing.T) {
			f := setup(t)
			p := f.artifact(t, f.repo)
			f.enroll(t, f.repo, false)
			f.age(t)
			plan := f.plan(t)
			switch scenario {
			case "expired":
				f.now = f.now.Add(time.Hour)
			case "policy":
				f.a.Config.ArtifactIdleHours++
			case "interrupted":
				if e := atomicJSON(filepath.Join(f.a.StateDir, "journal", plan.ID+".json"), Result{Status: "running"}); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := f.a.Apply(plan.ID, false); e == nil {
				t.Fatal("plan accepted")
			}
			exists(t, p, true)
		})
	}
}
func TestPressureSweepAndReportOnly(t *testing.T) {
	f := setup(t)
	p := f.artifact(t, f.repo)
	f.enroll(t, f.repo, false)
	f.age(t)
	f.a.Config.MinFreeBytes = ^uint64(0) - 1
	f.a.Config.TargetFreeBytes = ^uint64(0)
	if _, e := f.a.Sweep(); e != nil {
		t.Fatal(e)
	}
	exists(t, p, true)
	f.a.Config.Automatic = true
	if _, e := f.a.Sweep(); e != nil {
		t.Fatal(e)
	}
	exists(t, p, false)
}
func TestWorkspaceLockBlocksCleanup(t *testing.T) {
	f := setup(t)
	f.artifact(t, f.repo)
	f.enroll(t, f.repo, false)
	f.age(t)
	lock, e := f.a.workspaceLock(f.repo, true, false)
	if e != nil {
		t.Fatal(e)
	}
	defer unlock(lock)
	if len(f.plan(t).Actions) != 0 {
		t.Fatal("held lock ignored")
	}
}
func TestRunLeaseLifecycle(t *testing.T) {
	f := setup(t)
	f.enroll(t, f.repo, false)
	if e := f.a.Run(f.repo, []string{"sh", "-c", "exit 0"}); e != nil {
		t.Fatal(e)
	}
	ls, e := f.a.Leases()
	if e != nil || len(ls) != 0 {
		t.Fatalf("leases %v %v", ls, e)
	}
	if e := f.a.Run(f.repo, []string{"sh", "-c", "exit 7"}); e == nil {
		t.Fatal("lost child error")
	}
	ls, _ = f.a.Leases()
	if len(ls) != 0 {
		t.Fatal("normal failure leaked lease")
	}
}
func TestRunSignalRetainsLease(t *testing.T) {
	f := setup(t)
	f.enroll(t, f.repo, false)
	if e := f.a.Run(f.repo, []string{"sh", "-c", "kill -TERM $$"}); e == nil {
		t.Fatal("expected termination")
	}
	ls, e := f.a.Leases()
	if e != nil || len(ls) != 1 {
		t.Fatalf("leases %v %v", ls, e)
	}
}
func TestConfigAndService(t *testing.T) {
	f := setup(t)
	path := filepath.Join(t.TempDir(), "config.json")
	if e := SaveConfig(path, f.a.Config); e != nil {
		t.Fatal(e)
	}
	if _, e := LoadConfig(path); e != nil {
		t.Fatal(e)
	}
	write(t, path, `{"version":1,"typo":true}`)
	if _, e := LoadConfig(path); e == nil {
		t.Fatal("unknown config accepted")
	}
	for _, platform := range []string{"darwin", "linux"} {
		body, e := ServiceDefinition(platform, "/opt/Janitor & Tools/janitor", "/tmp/a%config", "/tmp/state")
		if e != nil || !strings.Contains(body, "daemon") {
			t.Fatalf("%s %s %v", platform, body, e)
		}
	}
	if _, e := ServiceDefinition("linux", "relative", "/a", "/b"); e == nil {
		t.Fatal("relative executable accepted")
	}
}
func TestDaemonCancellation(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var log strings.Builder
	if e := f.a.Daemon(ctx, &log); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(log.String(), "plan_id") {
		t.Fatal("missing sweep result")
	}
}
func TestUnknownStorageVisible(t *testing.T) {
	f := setup(t)
	write(t, filepath.Join(f.root, "unclassified", "huge"), strings.Repeat("x", 4096))
	i, e := f.a.Scan()
	if e != nil {
		t.Fatal(e)
	}
	if i.Roots[0].Unattributed.Logical != 4096 {
		t.Fatalf("%+v", i.Roots)
	}
}
func TestUnusualPath(t *testing.T) {
	f := setup(t)
	wt := f.worktree(t, "space-task")
	newPath := filepath.Join(f.root, "space and\nnewline")
	command(t, f.repo, "worktree", "move", wt, newPath)
	f.artifact(t, newPath)
	f.enroll(t, newPath, false)
	f.age(t)
	p := f.plan(t)
	if len(p.Actions) != 1 {
		t.Fatalf("%+v", p)
	}
	if _, e := f.a.Apply(p.ID, false); e != nil {
		t.Fatal(e)
	}
}
