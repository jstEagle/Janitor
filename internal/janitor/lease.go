package janitor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

type Lease struct {
	ID      string    `json:"id"`
	Path    string    `json:"path"`
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
}

func (a *App) leases() ([]Lease, error) {
	out := []Lease{}
	dir := filepath.Join(a.StateDir, "leases")
	entries, e := os.ReadDir(dir)
	if errors.Is(e, os.ErrNotExist) {
		return out, nil
	}
	if e != nil {
		return nil, e
	}
	for _, f := range entries {
		if filepath.Ext(f.Name()) != ".json" {
			continue
		}
		var l Lease
		if e = readJSON(filepath.Join(dir, f.Name()), &l); e != nil {
			return nil, e
		}
		if !validID(l.ID) || !filepath.IsAbs(l.Path) {
			return nil, errors.New("invalid lease record")
		}
		out = append(out, l)
	}
	return out, nil
}
func (a *App) Leases() ([]Lease, error) {
	l, e := a.stateLock()
	if e != nil {
		return nil, e
	}
	defer unlock(l)
	return a.leases()
}
func (a *App) ReleaseLease(leaseID string) error {
	if !validID(leaseID) {
		return errors.New("invalid lease ID")
	}
	l, e := a.stateLock()
	if e != nil {
		return e
	}
	defer unlock(l)
	path := filepath.Join(a.StateDir, "leases", leaseID+".json")
	var lease Lease
	if e = readJSON(path, &lease); e != nil {
		return e
	}
	w, e := a.workspaceLock(lease.Path, false, true)
	if e != nil {
		return errors.New("workspace is still locked by a running session")
	}
	defer unlock(w)
	if e = os.Remove(path); e != nil {
		return e
	}
	s, e := a.load()
	if e != nil {
		return e
	}
	delete(s.Observations, lease.Path)
	return a.save(s)
}

// Run holds a shared flock for the entire command. A persistent lease survives
// wrapper crashes: recovery always requires explicit operator acknowledgement.
func (a *App) Run(path string, args []string) error {
	if len(args) == 0 {
		return errors.New("run requires a command after --")
	}
	p, e := Canonical(path)
	if e != nil {
		return e
	}
	workdir := p
	root, e := git(p, "rev-parse", "--show-toplevel")
	if e != nil {
		return e
	}
	p, e = Canonical(trim(root))
	if e != nil {
		return e
	}
	global, e := a.stateLock()
	if e != nil {
		return e
	}
	s, e := a.load()
	if e != nil {
		unlock(global)
		return e
	}
	if _, ok := s.Enrollments[p]; !ok {
		unlock(global)
		return errors.New("enroll this workspace before using janitor run")
	}
	lock, e := a.workspaceLock(p, true, false)
	if e != nil {
		unlock(global)
		return e
	}
	defer lock.Close()
	lease := Lease{id(), p, os.Getpid(), a.Now()}
	record := filepath.Join(a.StateDir, "leases", lease.ID+".json")
	if e = atomicJSON(record, lease); e != nil {
		unlock(global)
		return e
	}
	delete(s.Observations, p)
	e = a.save(s)
	unlock(global)
	if e != nil {
		return e
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = workdir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Keep the foreground process group for interactive TTY compatibility. Pass the
	// lock to children; a background descendant retaining fd 3 keeps cleanup blocked.
	cmd.ExtraFiles = []*os.File{lock}
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(signals)
	if e = cmd.Start(); e != nil {
		_ = os.Remove(record)
		return e
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-signals:
				_ = cmd.Process.Signal(sig)
			case <-done:
				return
			}
		}
	}()
	runErr := cmd.Wait()
	close(done)
	// Do not remove the durable record after signal termination: descendants may
	// still run even if a shell closed the inherited descriptor.
	var exit *exec.ExitError
	if errors.As(runErr, &exit) {
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return fmt.Errorf("command interrupted; retained lease %s for review: %w", lease.ID, runErr)
		}
	}
	global, e = a.stateLock()
	if e != nil {
		return e
	}
	defer unlock(global)
	s, e = a.load()
	if e != nil {
		return e
	}
	delete(s.Observations, p)
	if e = a.save(s); e != nil {
		return e
	}
	if e = os.Remove(record); e != nil {
		return e
	}
	return runErr
}
