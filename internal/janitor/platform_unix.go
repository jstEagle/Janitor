//go:build darwin || linux

package janitor

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func lockFile(path string, shared, nonblock bool) (*os.File, error) {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	op := syscall.LOCK_EX
	if shared {
		op = syscall.LOCK_SH
	}
	if nonblock {
		op |= syscall.LOCK_NB
	}
	if e = syscall.Flock(int(f.Fd()), op); e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}
func unlock(f *os.File) {
	if f != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
}
func identity(info os.FileInfo) (string, uint64, int64, uint64) {
	s := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d:%d", s.Dev, s.Ino), uint64(s.Dev), int64(s.Blocks) * 512, uint64(s.Nlink)
}
func freeSpace(path string) (uint64, error) {
	var s syscall.Statfs_t
	if e := syscall.Statfs(path, &s); e != nil {
		return 0, e
	}
	return uint64(s.Bavail) * uint64(s.Bsize), nil
}
