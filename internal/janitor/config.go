package janitor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const Schema = 1

type Config struct {
	Version           int      `json:"version"`
	Roots             []string `json:"roots"`
	Protected         []string `json:"protected_paths"`
	ArtifactIdleHours int      `json:"artifact_idle_hours"`
	WorktreeIdleHours int      `json:"worktree_idle_hours"`
	IntervalSeconds   int      `json:"interval_seconds"`
	PlanTTLMinutes    int      `json:"plan_ttl_minutes"`
	MinFreeBytes      uint64   `json:"min_free_bytes"`
	TargetFreeBytes   uint64   `json:"target_free_bytes"`
	Automatic         bool     `json:"automatic"`
	RetireWorktrees   bool     `json:"retire_worktrees"`
}

func DefaultConfig() Config {
	return Config{Version: Schema, Roots: []string{}, Protected: []string{}, ArtifactIdleHours: 48, WorktreeIdleHours: 168, IntervalSeconds: 900, PlanTTLMinutes: 30, MinFreeBytes: 20 << 30, TargetFreeBytes: 30 << 30}
}
func DefaultConfigPath() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "janitor", "config.json")
}
func DefaultStatePath() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local", "state", "janitor")
}
func Canonical(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		h, e := os.UserHomeDir()
		if e != nil {
			return "", e
		}
		if p == "~" {
			p = h
		} else {
			p = filepath.Join(h, p[2:])
		}
	}
	p, e := filepath.Abs(p)
	if e != nil {
		return "", e
	}
	return filepath.EvalSymlinks(p)
}
func within(root, p string) bool {
	rel, e := filepath.Rel(root, p)
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
func overlap(a, b string) bool { return within(a, b) || within(b, a) }
func (c *Config) Validate() error {
	if c.Version != Schema {
		return fmt.Errorf("unsupported config version %d", c.Version)
	}
	if c.ArtifactIdleHours < 1 || c.WorktreeIdleHours < 1 || c.IntervalSeconds < 10 || c.PlanTTLMinutes < 1 {
		return errors.New("idle hours and plan TTL must be positive; interval must be at least 10 seconds")
	}
	if c.TargetFreeBytes < c.MinFreeBytes {
		return errors.New("target_free_bytes must be >= min_free_bytes")
	}
	for i, r := range c.Roots {
		p, e := Canonical(r)
		if e != nil {
			return fmt.Errorf("root %q: %w", r, e)
		}
		s, e := os.Stat(p)
		if e != nil || !s.IsDir() {
			return fmt.Errorf("root is not a directory: %s", p)
		}
		h, _ := os.UserHomeDir()
		if p == "/" || p == h {
			return errors.New("choose development directories, not / or your entire home")
		}
		c.Roots[i] = p
	}
	for i, r := range c.Protected {
		p, e := Canonical(r)
		if e != nil {
			return fmt.Errorf("protected path %q: %w", r, e)
		}
		c.Protected[i] = p
	}
	for i, a := range c.Roots {
		for _, b := range c.Roots[i+1:] {
			if overlap(a, b) {
				return fmt.Errorf("overlapping roots: %s and %s", a, b)
			}
		}
	}
	return nil
}
func LoadConfig(path string) (Config, error) {
	c := DefaultConfig()
	f, e := os.Open(path)
	if e != nil {
		return c, e
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return c, errors.New("config must contain one JSON object")
	}
	return c, c.Validate()
}
func SaveConfig(path string, c Config) error {
	if e := c.Validate(); e != nil {
		return e
	}
	return atomicJSON(path, c)
}
func (c Config) allows(p string) bool {
	for _, r := range c.Roots {
		if within(r, p) {
			return true
		}
	}
	return false
}
func (c Config) protects(p string) bool {
	for _, r := range c.Protected {
		if overlap(r, p) {
			return true
		}
	}
	return false
}
func hours(n int) time.Duration { return time.Duration(n) * time.Hour }
