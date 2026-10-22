package janitor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInboundWorkspaceSymlinkProtectsArtifact(t *testing.T) {
	for _, relative := range []bool{false, true} {
		t.Run(map[bool]string{false: "absolute", true: "relative"}[relative], func(t *testing.T) {
			f := setup(t)
			target := f.artifact(t, f.repo)
			consumer := f.worktree(t, "consumer")
			link := filepath.Join(consumer, "node_modules")
			destination := target
			if relative {
				var err error
				destination, err = filepath.Rel(consumer, target)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(destination, link); err != nil {
				t.Fatal(err)
			}
			f.enroll(t, f.repo, false)
			f.age(t)
			plan := f.plan(t)
			if len(plan.Actions) != 0 {
				t.Fatalf("referenced installation is disposable: %+v", plan.Actions)
			}
		})
	}
}

func TestNewInboundReferenceInvalidatesPlan(t *testing.T) {
	f := setup(t)
	target := f.artifact(t, f.repo)
	consumer := f.worktree(t, "consumer")
	f.enroll(t, f.repo, false)
	f.age(t)
	plan := f.plan(t)
	if len(plan.Actions) != 1 {
		t.Fatalf("expected unreferenced artifact: %+v", plan.Actions)
	}
	if err := os.Symlink(target, filepath.Join(consumer, "node_modules")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.Apply(plan.ID, false); err == nil {
		t.Fatal("new inbound reference did not invalidate saved plan")
	}
	exists(t, target, true)
}

func TestInboundReferenceProtectsWorktreeRetirement(t *testing.T) {
	f := setup(t)
	target := f.worktree(t, "installation")
	consumer := f.worktree(t, "consumer")
	if err := os.Symlink(filepath.Join(target, "package.json"), filepath.Join(consumer, "shared-package")); err != nil {
		t.Fatal(err)
	}
	f.enroll(t, target, true)
	f.age(t)
	if plan := f.plan(t); len(plan.Actions) != 0 {
		t.Fatalf("referenced source worktree can be retired: %+v", plan.Actions)
	}
}

func TestInternalSymlinksDoNotBlockCleanup(t *testing.T) {
	f := setup(t)
	target := f.artifact(t, f.repo)
	if err := os.Symlink("a", filepath.Join(target, "local-package")); err != nil {
		t.Fatal(err)
	}
	f.enroll(t, f.repo, false)
	f.age(t)
	plan := f.plan(t)
	if len(plan.Actions) != 1 {
		t.Fatalf("internal link blocked disposable artifact: %+v", plan.Actions)
	}
	if _, err := f.a.Apply(plan.ID, false); err != nil {
		t.Fatal(err)
	}
	exists(t, target, false)
}

func TestInboundReferenceDoesNotBlockUnrelatedWorkspace(t *testing.T) {
	f := setup(t)
	target := f.artifact(t, f.repo)
	consumer := f.worktree(t, "consumer")
	unrelated := f.worktree(t, "unrelated")
	unrelatedArtifact := f.artifact(t, unrelated)
	if err := os.Symlink(target, filepath.Join(consumer, "node_modules")); err != nil {
		t.Fatal(err)
	}
	f.enroll(t, f.repo, false)
	f.enroll(t, unrelated, false)
	f.age(t)
	plan := f.plan(t)
	if len(plan.Actions) != 1 || plan.Actions[0].Path != unrelatedArtifact {
		t.Fatalf("unexpected actions: %+v", plan.Actions)
	}
}

func TestIncompleteReferenceDiscoveryProtectsDestinations(t *testing.T) {
	workspaces := []Workspace{{Path: "/work/source", referencesUnknown: true}, {Path: "/work/target"}}
	protectInboundReferences(workspaces)
	if len(workspaces[1].Blocked) != 1 {
		t.Fatal("incomplete source discovery did not protect destination")
	}
}

func TestInboundReferencesUsePathBoundariesAndCommonAncestors(t *testing.T) {
	workspaces := []Workspace{
		{Path: "/work/consumer", linkTargets: map[string]bool{"/work/install/missing-package": true}},
		{Path: "/work/install"},
		{Path: "/work/install-other"},
	}
	protectInboundReferences(workspaces)
	if len(workspaces[1].Blocked) != 1 || len(workspaces[2].Blocked) != 0 {
		t.Fatal("broken destination or sibling path boundary mishandled")
	}
	workspaces = []Workspace{
		{Path: "/consumer", linkTargets: map[string]bool{"/work": true}},
		{Path: "/work/one"},
		{Path: "/work/two"},
	}
	protectInboundReferences(workspaces)
	if len(workspaces[1].Blocked) != 1 || len(workspaces[2].Blocked) != 1 {
		t.Fatal("link to ancestor did not protect descendant workspaces")
	}
}
