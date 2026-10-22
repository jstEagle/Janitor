package janitor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Sweep applies only when automatic mode is enabled and a configured volume is
// below its floor. It never weakens the same policy used by reviewed plans.
func (a *App) Sweep() (any, error) {
	l, e := a.stateLock()
	if e != nil {
		return nil, e
	}
	defer unlock(l)
	s, e := a.load()
	if e != nil {
		return nil, e
	}
	p, e := a.planLocked(&s)
	if e != nil {
		return nil, e
	}
	if !a.Config.Automatic {
		return p, nil
	}
	pressure := false
	for _, r := range p.Inventory.Roots {
		if r.FreeBytes < a.Config.MinFreeBytes {
			pressure = true
		}
	}
	if !pressure || len(p.Actions) == 0 {
		return p, nil
	}
	return a.applyLocked(p.ID, true, &s)
}
func (a *App) Daemon(ctx context.Context, out io.Writer) error {
	lock, e := lockFile(filepath.Join(a.StateDir, "daemon.lock"), false, true)
	if e != nil {
		return fmt.Errorf("another daemon holds this state directory: %w", e)
	}
	defer unlock(lock)
	ticker := time.NewTicker(time.Duration(a.Config.IntervalSeconds) * time.Second)
	defer ticker.Stop()
	enc := json.NewEncoder(out)
	for {
		result, e := a.Sweep()
		entry := map[string]any{"at": a.Now(), "automatic": a.Config.Automatic}
		if e != nil {
			entry["error"] = e.Error()
		} else {
			switch v := result.(type) {
			case Plan:
				entry["plan_id"] = v.ID
				entry["candidates"] = len(v.Actions)
				entry["warnings"] = v.Inventory.Warnings
			case Result:
				entry["result"] = v
			}
		}
		if err := enc.Encode(entry); err != nil {
			return err
		}
		a.pruneHistory()
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
func (a *App) pruneHistory() { // Bound completed journals and expired plans; preserve interrupted journals.
	l, e := a.stateLock()
	if e != nil {
		return
	}
	defer unlock(l)
	for _, sub := range []string{"plans", "journal"} {
		dir := filepath.Join(a.StateDir, sub)
		entries, e := os.ReadDir(dir)
		if e != nil {
			continue
		}
		for _, f := range entries {
			if filepath.Ext(f.Name()) != ".json" {
				continue
			}
			path := filepath.Join(dir, f.Name())
			inf, e := f.Info()
			if e != nil || a.Now().Sub(inf.ModTime()) < 30*24*time.Hour {
				continue
			}
			if sub == "journal" {
				var r Result
				if readJSON(path, &r) != nil || r.Status == "running" {
					continue
				}
			}
			_ = os.Remove(path)
		}
	}
}
