//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

// Package dblock exposes the SQLite-compatible byte-range lock used
// by Replica.applyLTXFile. External callers that mirror the LTX apply
// pipeline (e.g. shard-layer standby readers) take the same lock so
// they interlock with SQLite's own locking and with each other.
//
// The pending-byte + shared-region ranges are SQLite-internal
// addresses (sqlitePendingByte=0x40000000); locking those bytes
// blocks any SQLite reader/writer process that opens the same file,
// matching the granularity used by replicate's internal applyLTXFile.
package dblock

import (
	"os"

	"golang.org/x/sys/unix"
)

const (
	sqlitePendingByte = 0x40000000
	sqliteSharedFirst = sqlitePendingByte + 2
	sqliteSharedSize  = 510
)

// LockExclusive acquires the SQLite-compatible exclusive lock on f.
// Returns an error if the lock cannot be acquired (already held by
// another process, fd is invalid, etc).
func LockExclusive(f *os.File) error {
	fd := int(f.Fd())
	if err := setLock(fd, unix.F_WRLCK, sqlitePendingByte, 1); err != nil {
		return err
	}
	if err := setLock(fd, unix.F_WRLCK, sqliteSharedFirst, sqliteSharedSize); err != nil {
		_ = setLock(fd, unix.F_UNLCK, sqlitePendingByte, 1)
		return err
	}
	return nil
}

// Unlock releases the exclusive lock acquired by LockExclusive.
func Unlock(f *os.File) error {
	fd := int(f.Fd())
	err1 := setLock(fd, unix.F_UNLCK, sqliteSharedFirst, sqliteSharedSize)
	err2 := setLock(fd, unix.F_UNLCK, sqlitePendingByte, 1)
	if err1 != nil {
		return err1
	}
	return err2
}

func setLock(fd int, lockType int16, start, length int64) error {
	flock := unix.Flock_t{
		Type:   lockType,
		Whence: 0,
		Start:  start,
		Len:    length,
	}
	return unix.FcntlFlock(uintptr(fd), unix.F_SETLKW, &flock)
}
