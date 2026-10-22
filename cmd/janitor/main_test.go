package main

import (
	bytesbuf "bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIWorkflow(t *testing.T) {
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(base, "projects")
	repo := filepath.Join(root, "app")
	if e = os.MkdirAll(repo, 0700); e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{{"init", "-b", "main"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "initial"}} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if b, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("git %s %v", b, e)
		}
	}
	config := filepath.Join(base, "config.json")
	state := filepath.Join(base, "state")
	prefix := []string{"--config", config, "--state", state, "--json"}
	call := func(args ...string) string {
		t.Helper()
		var out bytesbuf.Buffer
		if e := run(append(append([]string{}, prefix...), args...), &out); e != nil {
			t.Fatalf("%v: %v", args, e)
		}
		return out.String()
	}
	call("init", "--root", root)
	var cfg map[string]any
	if e = json.Unmarshal([]byte(call("config")), &cfg); e != nil {
		t.Fatal(e)
	}
	if cfg["automatic"] != false {
		t.Fatal("unsafe default")
	}
	call("scan")
	call("status")
	call("explain", repo)
	call("enroll", repo, "--coordinated", "--artifacts", "node_modules")
	call("run", "--dir", repo, "--", "sh", "-c", "exit 0")
	call("leases")
	call("pin", repo, "--reason", "test")
	call("unpin", repo)
	var plan struct {
		ID      string `json:"id"`
		Actions []any  `json:"actions"`
	}
	if e = json.Unmarshal([]byte(call("plan")), &plan); e != nil {
		t.Fatal(e)
	}
	if len(plan.Actions) != 0 || plan.ID == "" {
		t.Fatalf("%+v", plan)
	}
	call("apply", plan.ID, "--yes")
	call("history")
	call("sweep")
	call("forget", repo)
	if !strings.Contains(call("service", "render"), "daemon") {
		t.Fatal("service missing command")
	}
	for _, args := range [][]string{{"init", "--root", root}, {"apply", plan.ID, "--yes"}, {"apply", plan.ID}, {"enroll", repo, "--artifacts", "node_modules"}, {"release", "../../escape", "--yes"}, {"unknown"}, {"sweep", "--dry-run"}, {"plan", "--force"}} {
		if e := run(append(append([]string{}, prefix...), args...), &bytesbuf.Buffer{}); e == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
func TestHelpAndVersion(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"--help"}, {"version"}} {
		var out bytesbuf.Buffer
		if e := run(args, &out); e != nil || out.Len() == 0 {
			t.Fatalf("%v %v", args, e)
		}
	}
}
