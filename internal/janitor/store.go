package janitor

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Enrollment struct {
	Path       string    `json:"path"`
	Artifacts  []string  `json:"artifacts"`
	Retire     bool      `json:"retire_worktree"`
	EnrolledAt time.Time `json:"enrolled_at"`
}
type Observation struct {
	Fingerprint string    `json:"fingerprint"`
	FirstSeen   time.Time `json:"first_seen"`
	LastChanged time.Time `json:"last_changed"`
	LastSeen    time.Time `json:"last_seen"`
}
type State struct {
	Version      int                    `json:"version"`
	Enrollments  map[string]Enrollment  `json:"enrollments"`
	Pins         map[string]string      `json:"pins"`
	Observations map[string]Observation `json:"observations"`
}
type App struct {
	Config   Config
	StateDir string
	Now      func() time.Time
}

func New(c Config, state string) (*App, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	p, e := filepath.Abs(state)
	if e != nil {
		return nil, e
	}
	home, _ := os.UserHomeDir()
	if p == "/" || p == home {
		return nil, errors.New("state requires a dedicated directory")
	}
	for _, r := range c.Roots {
		if overlap(r, p) {
			return nil, errors.New("state directory must be outside scan roots")
		}
	}
	if e = os.MkdirAll(p, 0700); e != nil {
		return nil, e
	}
	p, e = filepath.EvalSymlinks(p)
	if e != nil {
		return nil, e
	}
	if p == "/" || p == home {
		return nil, errors.New("state requires a dedicated directory")
	}
	for _, r := range c.Roots {
		if overlap(r, p) {
			return nil, errors.New("state directory must be outside scan roots")
		}
	}
	if e = os.Chmod(p, 0700); e != nil {
		return nil, e
	}
	return &App{Config: c, StateDir: p, Now: time.Now}, nil
}
func atomicJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	b = append(b, '\n')
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".janitor-write-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = f.Write(b); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func readJSON(path string, v any) error {
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, v)
}
func (a *App) load() (State, error) {
	s := State{Version: Schema, Enrollments: map[string]Enrollment{}, Pins: map[string]string{}, Observations: map[string]Observation{}}
	e := readJSON(filepath.Join(a.StateDir, "state.json"), &s)
	if errors.Is(e, os.ErrNotExist) {
		e = nil
	}
	if s.Version != Schema {
		return s, errors.New("unsupported state version")
	}
	if s.Enrollments == nil || s.Pins == nil || s.Observations == nil {
		return s, errors.New("invalid state maps")
	}
	return s, e
}
func (a *App) save(s State) error { return atomicJSON(filepath.Join(a.StateDir, "state.json"), s) }
func id() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func key(p string) string   { h := sha256.Sum256([]byte(p)); return hex.EncodeToString(h[:]) }
func digest(v any) string   { b, _ := json.Marshal(v); return key(string(b)) }
func validID(s string) bool { b, e := hex.DecodeString(s); return e == nil && len(b) == 16 }
func (a *App) stateLock() (*os.File, error) {
	return lockFile(filepath.Join(a.StateDir, "state.lock"), false, false)
}
func (a *App) workspaceLock(path string, shared, nonblock bool) (*os.File, error) {
	return lockFile(filepath.Join(a.StateDir, "locks", key(path)), shared, nonblock)
}
func (a *App) Enroll(path string, artifacts []string, retire bool) error {
	p, e := Canonical(path)
	if e != nil {
		return e
	}
	if !a.Config.allows(p) {
		return errors.New("workspace is outside configured roots")
	}
	if a.Config.protects(p) {
		return errors.New("workspace intersects a protected path")
	}
	for _, name := range artifacts {
		if _, ok := artifactMarkers[name]; !ok {
			return fmt.Errorf("unsupported artifact %q", name)
		}
	}
	root, e := git(p, "rev-parse", "--show-toplevel")
	if e != nil {
		return e
	}
	rp, e := Canonical(trim(root))
	if e != nil || rp != p {
		return errors.New("enroll the repository/worktree root")
	}
	l, e := a.stateLock()
	if e != nil {
		return e
	}
	defer unlock(l)
	s, e := a.load()
	if e != nil {
		return e
	}
	s.Enrollments[p] = Enrollment{p, artifacts, retire, a.Now()}
	delete(s.Observations, p)
	return a.save(s)
}
func (a *App) Pin(path, reason string, remove bool) error {
	p, e := Canonical(path)
	if e != nil {
		return e
	}
	l, e := a.stateLock()
	if e != nil {
		return e
	}
	defer unlock(l)
	s, e := a.load()
	if e != nil {
		return e
	}
	if remove {
		delete(s.Pins, p)
	} else {
		s.Pins[p] = reason
	}
	return a.save(s)
}
func (a *App) Forget(path string) error {
	p, e := Canonical(path)
	if e != nil {
		return e
	}
	l, e := a.stateLock()
	if e != nil {
		return e
	}
	defer unlock(l)
	s, e := a.load()
	if e != nil {
		return e
	}
	delete(s.Enrollments, p)
	return a.save(s)
}
