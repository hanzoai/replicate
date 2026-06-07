//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package internal

import (
	"os"

	"github.com/hanzoai/replicate/dblock"
)

// LockFileExclusive forwards to dblock.LockExclusive so the replica
// applier and external apply callers (shard layer) share one
// canonical SQLite-byte-range lock implementation.
func LockFileExclusive(f *os.File) error { return dblock.LockExclusive(f) }

// UnlockFile forwards to dblock.Unlock.
func UnlockFile(f *os.File) error { return dblock.Unlock(f) }
