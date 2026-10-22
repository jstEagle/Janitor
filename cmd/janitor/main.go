package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/jstEagle/Janitor/internal/janitor"
)

var version = "dev"

const usage = `Janitor — keep agent workspaces from filling your disk.

Usage: janitor [--config PATH] [--state DIR] [--json] COMMAND [OPTIONS]

  init --root DIR [--root DIR]   Create a report-only config
  scan                          Discover workspaces and measure storage
  status                        Show the last scan (does not rescan)
  explain PATH                  Show a workspace's protection and artifacts
  plan                          Save a fresh, reviewable cleanup plan
  apply ID --yes                 Revalidate and execute that exact plan
  enroll PATH --coordinated --artifacts .next,target [--retire]
                                Declare disposable outputs and coordinated use
  forget PATH                   Revoke a workspace's cleanup enrollment
  pin PATH [--reason TEXT]       Protect a workspace or subtree
  unpin PATH                    Remove an explicit pin
  run [--dir DIR] -- COMMAND     Hold an activity lease while a command runs
  leases                        List active or unresolved leases
  release ID --yes              Acknowledge an abandoned lease after inspection
  history                       Show cleanup results, including interruptions
  sweep                         Scan; clean under pressure if policy allows
  daemon                        Run periodic sweeps until interrupted
  service render|install|uninstall  Per-user launchd/systemd integration
  config                        Validate and print effective configuration
  version                       Print version

Global flags go BEFORE the command. No deletion occurs until policy enrollment
and an explicit apply, or enrollment plus automatic=true in your config.
`

type rootsFlag []string

func (r *rootsFlag) String() string     { return strings.Join(*r, ",") }
func (r *rootsFlag) Set(s string) error { *r = append(*r, s); return nil }
func flags(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(os.Stderr)
	return f
}
func main() {
	if e := run(os.Args[1:], os.Stdout); e != nil {
		fmt.Fprintln(os.Stderr, "janitor:", e)
		var exit *exec.ExitError
		if errors.As(e, &exit) && exit.ExitCode() > 0 {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	}
}
func run(args []string, out io.Writer) error {
	global := flags("janitor")
	configPath := global.String("config", janitor.DefaultConfigPath(), "configuration file")
	stateDir := global.String("state", janitor.DefaultStatePath(), "state directory")
	asJSON := global.Bool("json", false, "JSON output")
	global.Usage = func() { fmt.Fprint(out, usage) }
	if e := global.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return nil
		}
		return e
	}
	args = global.Args()
	if len(args) == 0 || args[0] == "help" {
		fmt.Fprint(out, usage)
		return nil
	}
	command := args[0]
	args = args[1:]
	switch command {
	case "version", "config", "status", "plan", "leases", "history", "sweep", "daemon":
		if len(args) > 0 {
			return fmt.Errorf("%s accepts no arguments; global flags go before the command", command)
		}
	}
	emit := func(v any) error {
		if *asJSON {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(v)
		}
		return human(out, v)
	}
	if command == "version" {
		return emit(map[string]string{"version": version, "go": runtime.Version(), "platform": runtime.GOOS + "/" + runtime.GOARCH})
	}
	if command == "init" {
		f := flags(command)
		var roots rootsFlag
		f.Var(&roots, "root", "development root (repeatable)")
		if e := f.Parse(args); e != nil {
			return e
		}
		if len(roots) == 0 || f.NArg() != 0 {
			return errors.New("init requires --root DIR (repeat for multiple roots)")
		}
		if _, e := os.Lstat(*configPath); !errors.Is(e, os.ErrNotExist) {
			return errors.New("config already exists or cannot be inspected; edit it explicitly")
		}
		c := janitor.DefaultConfig()
		c.Roots = roots
		if e := janitor.SaveConfig(*configPath, c); e != nil {
			return e
		}
		return emit(map[string]string{"config": *configPath, "mode": "report-only; run scan next"})
	}
	c, e := janitor.LoadConfig(*configPath)
	if e != nil {
		return fmt.Errorf("load config (use init first): %w", e)
	}
	a, e := janitor.New(c, *stateDir)
	if e != nil {
		return e
	}
	switch command {
	case "config":
		return emit(c)
	case "scan":
		if len(args) != 0 {
			return errors.New("scan accepts no arguments")
		}
		v, e := a.Scan()
		if e != nil {
			return e
		}
		return emit(v)
	case "status":
		v, e := a.Status()
		if e != nil {
			return fmt.Errorf("no readable inventory; run scan: %w", e)
		}
		return emit(v)
	case "explain":
		if len(args) != 1 {
			return errors.New("explain requires a workspace path")
		}
		p, e := janitor.Canonical(args[0])
		if e != nil {
			return e
		}
		v, e := a.Scan()
		if e != nil {
			return e
		}
		for _, w := range v.Workspaces {
			if w.Path == p {
				return emit(w)
			}
		}
		return errors.New("workspace not found in configured roots")
	case "plan":
		v, e := a.Plan()
		if e != nil {
			return e
		}
		return emit(v)
	case "apply":
		if len(args) != 2 || args[1] != "--yes" {
			return errors.New("usage: apply PLAN_ID --yes; review the saved plan first")
		}
		v, e := a.Apply(args[0], false)
		if v.PlanID != "" {
			if err := emit(v); err != nil {
				return err
			}
		}
		return e
	case "enroll":
		if len(args) == 0 {
			return errors.New("enroll requires a workspace path")
		}
		p := args[0]
		f := flags(command)
		coordinated := f.Bool("coordinated", false, "all activity uses janitor run")
		artifacts := f.String("artifacts", "", "comma-separated disposable artifact names")
		retire := f.Bool("retire", false, "allow clean worktree retirement when policy enables it")
		if e := f.Parse(args[1:]); e != nil {
			return e
		}
		if f.NArg() != 0 || !*coordinated {
			return errors.New("enrollment requires --coordinated: all sessions/builds must use janitor run")
		}
		var names []string
		if *artifacts != "" {
			names = strings.Split(*artifacts, ",")
		}
		if len(names) == 0 && !*retire {
			return fmt.Errorf("choose --artifacts from %s or --retire", strings.Join(janitor.ArtifactNames(), ","))
		}
		if e := a.Enroll(p, names, *retire); e != nil {
			return e
		}
		return emit(map[string]string{"enrolled": p, "activity": "use janitor run for every session and build; daemonizing commands are unsupported"})
	case "forget":
		if len(args) != 1 {
			return errors.New("forget requires a workspace path")
		}
		if e := a.Forget(args[0]); e != nil {
			return e
		}
		return emit(map[string]string{"forgotten": args[0]})
	case "pin", "unpin":
		if len(args) == 0 {
			return errors.New("pin/unpin requires a path")
		}
		f := flags(command)
		reason := f.String("reason", "manually protected", "reason")
		if e := f.Parse(args[1:]); e != nil {
			return e
		}
		if f.NArg() != 0 {
			return errors.New("unexpected arguments")
		}
		if e := a.Pin(args[0], *reason, command == "unpin"); e != nil {
			return e
		}
		return emit(map[string]string{command: args[0]})
	case "run":
		f := flags(command)
		dir := f.String("dir", ".", "working directory")
		if e := f.Parse(args); e != nil {
			return e
		}
		return a.Run(*dir, f.Args())
	case "leases":
		v, e := a.Leases()
		if e != nil {
			return e
		}
		return emit(v)
	case "release":
		if len(args) != 2 || args[1] != "--yes" {
			return errors.New("usage: release LEASE_ID --yes (first verify that its processes have stopped)")
		}
		if e := a.ReleaseLease(args[0]); e != nil {
			return e
		}
		return emit(map[string]string{"released": args[0], "observation": "idle grace period reset"})
	case "history":
		v, e := a.History()
		if e != nil {
			return e
		}
		return emit(v)
	case "sweep":
		v, e := a.Sweep()
		if v != nil {
			if err := emit(v); err != nil {
				return err
			}
		}
		return e
	case "daemon":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return a.Daemon(ctx, out)
	case "service":
		if len(args) != 1 {
			return errors.New("service requires render, install, or uninstall")
		}
		switch args[0] {
		case "render":
			exe, e := os.Executable()
			if e != nil {
				return e
			}
			cp, e := filepath.Abs(*configPath)
			if e != nil {
				return e
			}
			body, e := janitor.ServiceDefinition(runtime.GOOS, exe, cp, a.StateDir)
			if e != nil {
				return e
			}
			_, e = fmt.Fprint(out, body)
			return e
		case "install":
			p, e := janitor.InstallService(*configPath, a.StateDir)
			if e != nil {
				return e
			}
			return emit(map[string]string{"installed": p})
		case "uninstall":
			p, e := janitor.UninstallService()
			if e != nil {
				return e
			}
			return emit(map[string]string{"removed": p})
		default:
			return errors.New("unknown service operation")
		}
	default:
		return fmt.Errorf("unknown command %q; use help", command)
	}
}
func human(out io.Writer, v any) error {
	switch x := v.(type) {
	case janitor.Inventory:
		fmt.Fprintf(out, "Inventory %s · %d workspaces\n", x.At.Format(time.RFC3339), len(x.Workspaces))
		for _, r := range x.Roots {
			fmt.Fprintf(out, "  %q: %s free · %s outside discovered workspaces\n", r.Path, bytes(int64(r.FreeBytes)), bytes(r.Unattributed.Allocated))
		}
		for _, w := range x.Workspaces {
			state := "enrolled"
			if len(w.Blocked) > 0 {
				state = w.Blocked[0]
			}
			fmt.Fprintf(out, "  %8s  %q  [%s]\n", bytes(w.Size.Allocated), w.Path, state)
		}
		for _, warning := range x.Warnings {
			fmt.Fprintf(out, "  warning: %s\n", warning)
		}
		fmt.Fprintln(out, "Sizes are allocated estimates; shared blocks and snapshots affect actual reclamation.")
	case janitor.Plan:
		fmt.Fprintf(out, "Plan %s · %d actions · expires %s\n", x.ID, len(x.Actions), x.Expires.Format(time.RFC3339))
		for _, act := range x.Actions {
			fmt.Fprintf(out, "  %8s  %-8s %q\n", bytes(act.Estimate), act.Kind, act.Path)
		}
		if len(x.Actions) == 0 {
			fmt.Fprintln(out, "No eligible cleanup. Use explain PATH for protections; new observations must complete the idle grace period.")
		} else {
			fmt.Fprintf(out, "Review JSON for full evidence. Apply: janitor apply %s --yes\n", x.ID)
		}
	case janitor.Result:
		fmt.Fprintf(out, "Cleanup %s: %s · %d completed · %d skipped\n", x.PlanID, x.Status, len(x.Completed), len(x.Skipped))
		for root, before := range x.FreeBefore {
			after := x.FreeAfter[root]
			fmt.Fprintf(out, "  %q: observed free-space change %+d bytes\n", root, int64(after)-int64(before))
		}
		for _, e := range x.Errors {
			fmt.Fprintln(out, "  error:", e)
		}
	default:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	return nil
}
func bytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	f := float64(n)
	for _, u := range units {
		f /= 1024
		if f < 1024 || u == "TiB" {
			return fmt.Sprintf("%.1f %s", f, u)
		}
	}
	return ""
}
