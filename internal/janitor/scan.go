package janitor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Recognition is deliberately narrower than "anything ignored by Git".
var artifactMarkers = map[string][]string{
	"node_modules": {"package.json"}, ".next": {"package.json"}, ".nuxt": {"package.json"}, ".turbo": {"package.json"},
	"target": {"Cargo.toml"}, ".build": {"Package.swift"}, ".venv": {"pyproject.toml", "requirements.txt", "Pipfile"},
}

func ArtifactNames() []string {
	var v []string
	for k := range artifactMarkers {
		v = append(v, k)
	}
	sort.Strings(v)
	return v
}

type Size struct {
	Logical         int64 `json:"logical_bytes"`
	Allocated       int64 `json:"allocated_bytes"`
	ReclaimEstimate int64 `json:"reclaim_estimate_bytes"`
	Files           int64 `json:"files"`
}
type Artifact struct {
	Path        string   `json:"path"`
	Kind        string   `json:"kind"`
	Size        Size     `json:"size"`
	Fingerprint string   `json:"fingerprint"`
	Blocked     []string `json:"blocked"`
}
type Workspace struct {
	linkTargets       map[string]bool
	referencesUnknown bool
	Path              string     `json:"path"`
	Identity          string     `json:"identity"`
	Size              Size       `json:"size"`
	Fingerprint       string     `json:"fingerprint"`
	Git               GitInfo    `json:"git"`
	Artifacts         []Artifact `json:"artifacts"`
	LastChanged       time.Time  `json:"last_changed"`
	Enrolled          bool       `json:"enrolled"`
	Blocked           []string   `json:"blocked"`
	RetirementBlocked []string   `json:"retirement_blocked"`
}
type UnattributedPath struct {
	Path string `json:"path"`
	Size Size   `json:"size"`
}

type RootUsage struct {
	LargestUnattributed []UnattributedPath `json:"largest_unattributed"`
	Path                string             `json:"path"`
	Unattributed        Size               `json:"unattributed"`
	FreeBytes           uint64             `json:"free_bytes"`
}
type RepositoryUsage struct {
	Path string `json:"path"`
	Size Size   `json:"size"`
}

type Inventory struct {
	Repositories []RepositoryUsage `json:"repositories"`
	Version      int               `json:"version"`
	At           time.Time         `json:"at"`
	Workspaces   []Workspace       `json:"workspaces"`
	Roots        []RootUsage       `json:"roots"`
	Warnings     []string          `json:"warnings"`
}
type treeResult struct {
	LinkTargets map[string]bool
	Size        Size
	Fingerprint string
	Identity    string
	Newest      time.Time
	Issues      []string
}

func measure(path string, skip map[string]bool, ignoreGit bool) treeResult {
	r := treeResult{LinkTargets: map[string]bool{}}
	info, e := os.Lstat(path)
	if e != nil {
		r.Issues = append(r.Issues, e.Error())
		return r
	}
	var device uint64
	r.Identity, device, _, _ = identity(info)
	h := sha256.New()
	seen := map[string]bool{}
	e = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			r.Issues = append(r.Issues, err.Error())
			return nil
		}
		if p != path && skip[p] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if ignoreGit && p != path && d.Name() == ".git" {
			if filepath.Dir(p) != path {
				r.Issues = append(r.Issues, "nested repository: "+filepath.Dir(p))
			} else {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		inf, err := d.Info()
		if err != nil {
			r.Issues = append(r.Issues, err.Error())
			return nil
		}
		ident, dev, allocated, links := identity(inf)
		if dev != device {
			r.Issues = append(r.Issues, "mount boundary: "+p)
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(path, p)
		fmt.Fprintf(h, "%q %s %d %d %d\n", rel, ident, inf.Mode(), inf.Size(), inf.ModTime().UnixNano())
		if inf.ModTime().After(r.Newest) {
			r.Newest = inf.ModTime()
		}
		if inf.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				r.Issues = append(r.Issues, err.Error())
			}
			fmt.Fprintf(h, "link %q\n", target)
			if err == nil {
				if !filepath.IsAbs(target) {
					target = filepath.Join(filepath.Dir(p), target)
				}
				r.LinkTargets[filepath.Clean(target)] = true
			}
			return nil
		}
		if !inf.IsDir() && !inf.Mode().IsRegular() {
			r.Issues = append(r.Issues, "special file: "+p)
			return nil
		}
		if inf.Mode().IsRegular() && !seen[ident] {
			seen[ident] = true
			r.Size.Files++
			r.Size.Logical += inf.Size()
			r.Size.Allocated += allocated
			if links == 1 {
				r.Size.ReclaimEstimate += allocated
			}
		}
		return nil
	})
	if e != nil {
		r.Issues = append(r.Issues, e.Error())
	}
	r.Fingerprint = hex.EncodeToString(h.Sum(nil))
	return r
}
func recognized(path string) bool {
	for _, m := range artifactMarkers[filepath.Base(path)] {
		s, e := os.Lstat(filepath.Join(filepath.Dir(path), m))
		if e == nil && s.Mode().IsRegular() {
			return true
		}
	}
	return false
}
func (a *App) discover() (map[string]bool, []string) {
	found := map[string]bool{}
	var warnings []string
	for _, root := range a.Config.Roots {
		rootInfo, e := os.Stat(root)
		if e != nil {
			warnings = append(warnings, e.Error())
			continue
		}
		_, device, _, _ := identity(rootInfo)
		e = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				warnings = append(warnings, err.Error())
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			inf, err := d.Info()
			if err != nil {
				warnings = append(warnings, err.Error())
				return filepath.SkipDir
			}
			_, dev, _, _ := identity(inf)
			if dev != device {
				warnings = append(warnings, "not crossing mount boundary: "+p)
				return filepath.SkipDir
			}
			if p != root && (d.Name() == ".git" || recognized(p)) {
				return filepath.SkipDir
			}
			if _, e := os.Lstat(filepath.Join(p, ".git")); e == nil {
				found[p] = true
				return filepath.SkipDir
			}
			return nil
		})
		if e != nil {
			warnings = append(warnings, e.Error())
		}
	}
	var initial []string
	for p := range found {
		initial = append(initial, p)
	}
	for _, p := range initial {
		paths, e := worktreePaths(p)
		if e != nil {
			warnings = append(warnings, e.Error())
			continue
		}
		for _, q := range paths {
			cp, e := Canonical(q)
			if e != nil {
				warnings = append(warnings, "unavailable registered worktree: "+q)
				continue
			}
			if !a.Config.allows(cp) {
				warnings = append(warnings, "registered worktree outside configured roots: "+cp)
				continue
			}
			found[cp] = true
		}
	}
	return found, warnings
}
func (a *App) inspect(p string, s *State, skip map[string]bool) Workspace {
	w := Workspace{Path: p, Artifacts: []Artifact{}, Blocked: []string{}, RetirementBlocked: []string{}}
	t := measure(p, skip, true)
	w.Size = t.Size
	w.Identity = t.Identity
	w.Fingerprint = t.Fingerprint
	w.Blocked = append(w.Blocked, t.Issues...)
	w.linkTargets = t.LinkTargets
	w.referencesUnknown = len(t.Issues) > 0
	canonical, e := Canonical(p)
	if e != nil || canonical != p {
		w.Blocked = append(w.Blocked, "workspace path changed or contains a symlink")
	}
	w.Git, e = inspectGit(p)
	if e != nil {
		w.Blocked = append(w.Blocked, e.Error())
	}
	w.Fingerprint = digest([]string{w.Fingerprint, w.Git.Head, w.Git.Branch})
	en, enrolled := s.Enrollments[p]
	w.Enrolled = enrolled
	if !enrolled {
		w.Blocked = append(w.Blocked, "not enrolled for coordinated cleanup")
	}
	if a.Config.protects(p) {
		w.Blocked = append(w.Blocked, "protected path")
	}
	for pin, reason := range s.Pins {
		if overlap(pin, p) {
			w.Blocked = append(w.Blocked, "pinned: "+reason)
		}
	}
	if w.Git.Locked {
		w.Blocked = append(w.Blocked, "Git worktree is locked")
	}
	leases, e := a.leases()
	if e != nil {
		w.Blocked = append(w.Blocked, "lease state unreadable: "+e.Error())
	}
	for _, l := range leases {
		if overlap(l.Path, p) {
			w.Blocked = append(w.Blocked, "active or unresolved lease: "+l.ID)
		}
	}
	lock, e := a.workspaceLock(p, false, true)
	if e != nil {
		w.Blocked = append(w.Blocked, "workspace lock is held")
	} else {
		unlock(lock)
	}
	now := a.Now()
	obs, ok := s.Observations[p]
	if !ok {
		obs = Observation{FirstSeen: now, LastChanged: now}
	}
	if obs.Fingerprint != w.Fingerprint {
		obs.LastChanged = now
	}
	if t.Newest.After(obs.LastChanged) {
		obs.LastChanged = t.Newest
	}
	obs.Fingerprint = w.Fingerprint
	obs.LastSeen = now
	s.Observations[p] = obs
	w.LastChanged = obs.LastChanged
	if !enrolled || !en.Retire || !a.Config.RetireWorktrees {
		w.RetirementBlocked = append(w.RetirementBlocked, "worktree retirement is not enabled in both policy and enrollment")
	}
	if ownerManaged(p) {
		w.RetirementBlocked = append(w.RetirementBlocked, "app-owned worktree: retire through its owner")
	}
	if w.Git.Main {
		w.RetirementBlocked = append(w.RetirementBlocked, "primary checkout")
	}
	if w.Git.Dirty {
		w.RetirementBlocked = append(w.RetirementBlocked, "modified or untracked files")
	}
	if w.Git.Ignored {
		w.RetirementBlocked = append(w.RetirementBlocked, "ignored data must be removed or preserved explicitly")
	}
	if !w.Git.Recoverable {
		w.RetirementBlocked = append(w.RetirementBlocked, w.Git.Reason)
	}
	// Nested registered worktrees must not disappear inside a parent retirement.
	for nested := range skip {
		if nested != p && within(p, nested) {
			w.RetirementBlocked = append(w.RetirementBlocked, "contains another registered worktree")
		}
	}
	e = filepath.WalkDir(p, func(q string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if q != p && skip[q] {
			return filepath.SkipDir
		}
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		if q != p {
			if _, e := os.Lstat(filepath.Join(q, ".git")); e == nil {
				return filepath.SkipDir
			}
		}
		if !recognized(q) {
			return nil
		}
		m := measure(q, nil, true)
		ar := Artifact{Path: q, Kind: filepath.Base(q), Size: m.Size, Fingerprint: m.Fingerprint, Blocked: append([]string{}, m.Issues...)}
		if _, err := os.Lstat(filepath.Join(q, ".git")); err == nil {
			ar.Blocked = append(ar.Blocked, "artifact contains a repository")
		}
		approved := false
		for _, name := range en.Artifacts {
			if name == ar.Kind {
				approved = true
			}
		}
		if !approved {
			ar.Blocked = append(ar.Blocked, "artifact kind not explicitly enrolled")
		}
		rel, _ := filepath.Rel(p, q)
		tracked, e := artifactTracked(p, rel)
		if e != nil {
			ar.Blocked = append(ar.Blocked, e.Error())
		} else if tracked {
			ar.Blocked = append(ar.Blocked, "contains Git-tracked files")
		}
		w.Artifacts = append(w.Artifacts, ar)
		return filepath.SkipDir
	})
	if e != nil {
		w.Blocked = append(w.Blocked, e.Error())
	}
	return w
}
func (a *App) scanLocked(s *State) (Inventory, error) {
	inv := Inventory{Version: Schema, At: a.Now(), Workspaces: []Workspace{}, Roots: []RootUsage{}, Warnings: []string{}}
	paths, warnings := a.discover()
	inv.Warnings = append(inv.Warnings, warnings...)
	var sorted []string
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	for _, p := range sorted {
		inv.Workspaces = append(inv.Workspaces, a.inspect(p, s, paths))
	}
	protectInboundReferences(inv.Workspaces)
	repos := map[string]bool{}
	for _, w := range inv.Workspaces {
		if w.Git.CommonDir != "" && !repos[w.Git.CommonDir] {
			repos[w.Git.CommonDir] = true
			m := measure(w.Git.CommonDir, nil, false)
			inv.Repositories = append(inv.Repositories, RepositoryUsage{w.Git.CommonDir, m.Size})
			inv.Warnings = append(inv.Warnings, m.Issues...)
		}
	}
	for _, root := range a.Config.Roots {
		m := measure(root, paths, false)
		inv.Warnings = append(inv.Warnings, m.Issues...)
		free, e := freeSpace(root)
		if e != nil {
			inv.Warnings = append(inv.Warnings, e.Error())
		}
		largest := []UnattributedPath{}
		entries, err := os.ReadDir(root)
		if err == nil {
			for _, entry := range entries {
				p := filepath.Join(root, entry.Name())
				if paths[p] || entry.Type()&os.ModeSymlink != 0 {
					continue
				}
				m := measure(p, paths, false)
				if m.Size.Logical > 0 {
					largest = append(largest, UnattributedPath{p, m.Size})
				}
			}
		}
		sort.Slice(largest, func(i, j int) bool { return largest[i].Size.Allocated > largest[j].Size.Allocated })
		if len(largest) > 20 {
			largest = largest[:20]
		}
		inv.Roots = append(inv.Roots, RootUsage{Path: root, Unattributed: m.Size, FreeBytes: free, LargestUnattributed: largest})
	}
	// Retain recently missing observations for 30 days, without unbounded history growth.
	for p, o := range s.Observations {
		if !paths[p] && a.Now().Sub(o.LastSeen) > 30*24*time.Hour {
			delete(s.Observations, p)
		}
	}
	if e := a.save(*s); e != nil {
		return inv, e
	}
	return inv, atomicJSON(filepath.Join(a.StateDir, "inventory.json"), inv)
}
func (a *App) Scan() (Inventory, error) {
	l, e := a.stateLock()
	if e != nil {
		return Inventory{}, e
	}
	defer unlock(l)
	s, e := a.load()
	if e != nil {
		return Inventory{}, e
	}
	return a.scanLocked(&s)
}
func (a *App) Status() (Inventory, error) {
	var i Inventory
	e := readJSON(filepath.Join(a.StateDir, "inventory.json"), &i)
	return i, e
}
func ownerManaged(path string) bool {
	p := filepath.ToSlash(path)
	for _, part := range []string{"/.codex/worktrees/", "/.claude/worktrees/", "/.cursor/worktrees/"} {
		if strings.Contains(p, part) {
			return true
		}
	}
	return false
}
