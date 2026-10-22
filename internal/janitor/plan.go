package janitor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Action struct {
	Kind                 string `json:"kind"`
	Path                 string `json:"path"`
	Workspace            string `json:"workspace"`
	WorkspaceIdentity    string `json:"workspace_identity"`
	Fingerprint          string `json:"fingerprint"`
	WorkspaceFingerprint string `json:"workspace_fingerprint"`
	Estimate             int64  `json:"reclaim_estimate_bytes"`
	Reason               string `json:"reason"`
}
type Plan struct {
	Version      int       `json:"version"`
	ID           string    `json:"id"`
	Created      time.Time `json:"created"`
	Expires      time.Time `json:"expires"`
	PolicyDigest string    `json:"policy_digest"`
	Actions      []Action  `json:"actions"`
	Inventory    Inventory `json:"inventory"`
}
type Result struct {
	PlanID     string            `json:"plan_id"`
	Started    time.Time         `json:"started"`
	Finished   time.Time         `json:"finished"`
	Completed  []Action          `json:"completed"`
	Skipped    []string          `json:"skipped"`
	Errors     []string          `json:"errors"`
	FreeBefore map[string]uint64 `json:"free_before"`
	FreeAfter  map[string]uint64 `json:"free_after"`
	Status     string            `json:"status"`
}

func policyDigest(c Config, s State) string {
	return digest(struct {
		Config      Config
		Enrollments map[string]Enrollment
		Pins        map[string]string
	}{c, s.Enrollments, s.Pins})
}
func (a *App) candidates(inv Inventory) []Action {
	out := []Action{}
	for _, w := range inv.Workspaces {
		if len(w.Blocked) > 0 {
			continue
		}
		idle := a.Now().Sub(w.LastChanged)
		if idle >= hours(a.Config.WorktreeIdleHours) && len(w.RetirementBlocked) == 0 && !ownerManaged(w.Path) {
			out = append(out, Action{"worktree", w.Path, w.Path, w.Identity, w.Fingerprint, w.Fingerprint, w.Size.ReclaimEstimate, "inactive, clean, locally preserved branch with remote-tracking reachability"})
			continue
		}
		if idle < hours(a.Config.ArtifactIdleHours) {
			continue
		}
		for _, ar := range w.Artifacts {
			if len(ar.Blocked) > 0 {
				continue
			}
			out = append(out, Action{"artifact", ar.Path, w.Path, w.Identity, ar.Fingerprint, w.Fingerprint, ar.Size.ReclaimEstimate, "explicitly disposable artifact in an idle enrolled workspace"})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Estimate == out[j].Estimate {
			return out[i].Path < out[j].Path
		}
		return out[i].Estimate > out[j].Estimate
	})
	return out
}
func (a *App) planLocked(s *State) (Plan, error) {
	inv, e := a.scanLocked(s)
	if e != nil {
		return Plan{}, e
	}
	p := Plan{Version: Schema, ID: id(), Created: a.Now(), Expires: a.Now().Add(time.Duration(a.Config.PlanTTLMinutes) * time.Minute), PolicyDigest: policyDigest(a.Config, *s), Actions: a.candidates(inv), Inventory: inv}
	if e = atomicJSON(filepath.Join(a.StateDir, "plans", p.ID+".json"), p); e != nil {
		return p, e
	}
	return p, nil
}
func (a *App) Plan() (Plan, error) {
	l, e := a.stateLock()
	if e != nil {
		return Plan{}, e
	}
	defer unlock(l)
	s, e := a.load()
	if e != nil {
		return Plan{}, e
	}
	return a.planLocked(&s)
}
func (a *App) Apply(planID string, pressure bool) (Result, error) {
	if !validID(planID) {
		return Result{}, errors.New("invalid plan ID")
	}
	l, e := a.stateLock()
	if e != nil {
		return Result{}, e
	}
	defer unlock(l)
	s, e := a.load()
	if e != nil {
		return Result{}, e
	}
	return a.applyLocked(planID, pressure, &s)
}
func (a *App) applyLocked(planID string, pressure bool, s *State) (Result, error) {
	var p Plan
	if e := readJSON(filepath.Join(a.StateDir, "plans", planID+".json"), &p); e != nil {
		return Result{}, e
	}
	if p.ID != planID || p.Version != Schema {
		return Result{}, errors.New("invalid plan")
	}
	if !a.Now().Before(p.Expires) || a.Now().Before(p.Created) {
		return Result{}, errors.New("plan expired or clock moved backward; create a new plan")
	}
	if p.PolicyDigest != policyDigest(a.Config, *s) {
		return Result{}, errors.New("policy changed; create a new plan")
	}
	journal := filepath.Join(a.StateDir, "journal", planID+".json")
	if _, e := os.Lstat(journal); !errors.Is(e, os.ErrNotExist) {
		return Result{}, errors.New("plan was already attempted; inspect history and create a new plan")
	}
	// Reconcile every target before making any mutation. A changed tree invalidates
	// the entire plan; candidates are never accepted solely from the saved file.
	inv, e := a.scanLocked(s)
	if e != nil {
		return Result{}, e
	}
	fresh := a.candidates(inv)
	eligible := map[string]Action{}
	for _, act := range fresh {
		eligible[act.Path] = act
	}
	for _, act := range p.Actions {
		f, ok := eligible[act.Path]
		if !ok || digest(f) != digest(act) {
			return Result{}, fmt.Errorf("plan no longer applies to %s; create a new plan", act.Path)
		}
	}
	result := Result{PlanID: planID, Started: a.Now(), Completed: []Action{}, Skipped: []string{}, Errors: []string{}, FreeBefore: map[string]uint64{}, FreeAfter: map[string]uint64{}, Status: "running"}
	for _, r := range a.Config.Roots {
		free, e := freeSpace(r)
		if e == nil {
			result.FreeBefore[r] = free
		}
	}
	if e = atomicJSON(journal, result); e != nil {
		return result, e
	}
	touched := map[string]bool{}
	pressureVolumes := map[uint64]bool{}
	if pressure {
		for _, root := range a.Config.Roots {
			info, err := os.Stat(root)
			if err != nil {
				continue
			}
			_, dev, _, _ := identity(info)
			if result.FreeBefore[root] < a.Config.MinFreeBytes {
				pressureVolumes[dev] = true
			}
		}
	}
	for _, act := range p.Actions {
		if pressure {
			info, err := os.Stat(act.Workspace)
			if err != nil {
				result.Errors = append(result.Errors, err.Error())
				continue
			}
			_, dev, _, _ := identity(info)
			if !pressureVolumes[dev] {
				result.Skipped = append(result.Skipped, act.Path+": volume was not below its floor")
				continue
			}
			free, err := freeSpace(act.Workspace)
			if err != nil {
				result.Errors = append(result.Errors, err.Error())
				continue
			}
			if free >= a.Config.TargetFreeBytes {
				result.Skipped = append(result.Skipped, act.Path+": free-space target reached")
				continue
			}
		}
		lock, err := a.workspaceLock(act.Workspace, false, true)
		if err != nil {
			result.Errors = append(result.Errors, act.Path+": workspace became active")
			continue
		}
		err = a.execute(act)
		unlock(lock)
		if err != nil {
			result.Errors = append(result.Errors, act.Path+": "+err.Error())
		} else {
			result.Completed = append(result.Completed, act)
			touched[act.Workspace] = true
		}
		if e = atomicJSON(journal, result); e != nil {
			return result, fmt.Errorf("journal update failed; cleanup stopped: %w", e)
		}
	}
	for path := range touched {
		delete(s.Observations, path)
	}
	if e = a.save(*s); e != nil {
		result.Errors = append(result.Errors, e.Error())
	}
	for _, r := range a.Config.Roots {
		free, e := freeSpace(r)
		if e == nil {
			result.FreeAfter[r] = free
		}
	}
	result.Finished = a.Now()
	result.Status = "completed"
	if len(result.Errors) > 0 {
		result.Status = "partial"
	}
	if e = atomicJSON(journal, result); e != nil {
		return result, e
	}
	if len(result.Errors) > 0 {
		return result, errors.New("cleanup incomplete; see errors in result")
	}
	return result, nil
}
func (a *App) execute(act Action) error {
	cp, e := Canonical(act.Workspace)
	if e != nil || cp != act.Workspace || !a.Config.allows(cp) || a.Config.protects(cp) {
		return errors.New("workspace boundary changed")
	}
	info, e := os.Lstat(cp)
	if e != nil {
		return e
	}
	ident, _, _, _ := identity(info)
	if ident != act.WorkspaceIdentity {
		return errors.New("workspace identity changed")
	}
	currentGit, err := inspectGit(cp)
	if err != nil {
		return err
	}
	if currentGit.Locked {
		return errors.New("Git worktree became locked")
	}
	if act.Kind == "worktree" {
		if ownerManaged(cp) {
			return errors.New("app-owned worktree requires owner-managed retirement")
		}
		g, e := inspectGit(cp)
		if e != nil {
			return e
		}
		if g.Main || g.Locked || g.Dirty || g.Ignored || !g.Recoverable {
			return errors.New("Git retirement preconditions changed")
		}
		m := measure(cp, nil, true)
		if len(m.Issues) > 0 || digest([]string{m.Fingerprint, g.Head, g.Branch}) != act.Fingerprint {
			return errors.New("worktree contents changed")
		}
		// Git itself refuses dirty and locked worktrees. Never use --force or delete branches.
		_, e = git(cp, "worktree", "remove", "--", cp)
		return e
	}
	if act.Kind != "artifact" || !within(cp, act.Path) || act.Path == cp {
		return errors.New("invalid action boundary")
	}
	rel, _ := filepath.Rel(cp, act.Path)
	tracked, e := artifactTracked(cp, rel)
	if e != nil {
		return e
	}
	if tracked {
		return errors.New("artifact now contains tracked files")
	}
	if !recognized(act.Path) {
		return errors.New("artifact marker changed")
	}
	m := measure(act.Path, nil, true)
	if len(m.Issues) > 0 || m.Fingerprint != act.Fingerprint {
		return errors.New("artifact contents changed")
	}
	// Open the parent once, then operate relative to that descriptor. Rename first
	// so a new build at the original name cannot be swept into this deletion.
	parent := filepath.Dir(act.Path)
	canonical, e := Canonical(parent)
	if e != nil || canonical != parent {
		return errors.New("artifact parent changed")
	}
	workspaceRoot, e := os.OpenRoot(cp)
	if e != nil {
		return e
	}
	defer workspaceRoot.Close()
	pinnedInfo, e := workspaceRoot.Stat(".")
	if e != nil {
		return e
	}
	pinnedID, _, _, _ := identity(pinnedInfo)
	if pinnedID != act.WorkspaceIdentity {
		return errors.New("workspace replaced while opening root")
	}
	parentRel, _ := filepath.Rel(cp, parent)
	root, e := workspaceRoot.OpenRoot(parentRel)
	if e != nil {
		return e
	}
	defer root.Close()
	name := filepath.Base(act.Path)
	original, e := root.Lstat(name)
	if e != nil {
		return e
	}
	ident, _, _, _ = identity(original)
	if ident != m.Identity || !original.IsDir() {
		return errors.New("artifact identity changed")
	}
	quarantine := ".janitor-removing-" + id()
	if e = root.Rename(name, quarantine); e != nil {
		return e
	}
	captured, e := root.Lstat(quarantine)
	if e != nil {
		return e
	}
	capturedID, _, _, _ := identity(captured)
	if capturedID != ident {
		return fmt.Errorf("captured identity changed; retained %s for manual recovery", filepath.Join(parent, quarantine))
	}
	// This name is deliberately not recognized by the scanner. On interruption it
	// remains visible as unattributed workspace data, never swept automatically.
	if e = root.RemoveAll(quarantine); e != nil {
		return fmt.Errorf("deletion interrupted; remaining data at %s: %w", filepath.Join(parent, quarantine), e)
	}
	return nil
}
func (a *App) History() ([]Result, error) {
	out := []Result{}
	entries, e := os.ReadDir(filepath.Join(a.StateDir, "journal"))
	if errors.Is(e, os.ErrNotExist) {
		return out, nil
	}
	if e != nil {
		return nil, e
	}
	for _, f := range entries {
		if !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		var r Result
		if e = readJSON(filepath.Join(a.StateDir, "journal", f.Name()), &r); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out, nil
}
