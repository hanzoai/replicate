//go:build windows

package dblock

import (
	"os"

	"golang.org/x/sys/windows"
)

const (
	sqlitePendingByte = 0x40000000
	sqliteSharedFirst = sqlitePendingByte + 2
	sqliteSharedSize  = 510
)

// LockExclusive acquires the SQLite-compatible exclusive lock on f.
// Blocking — matches upstream replicate.applyLTXFile behavior.
func LockExclusive(f *os.File) error {
	h := windows.Handle(f.Fd())
	pendingOL := windows.Overlapped{Offset: sqlitePendingByte}
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &pendingOL); err != nil {
		return err
	}
	sharedOL := windows.Overlapped{Offset: sqliteSharedFirst}
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, sqliteSharedSize, 0, &sharedOL); err != nil {
		pendingOL2 := windows.Overlapped{Offset: sqlitePendingByte}
		_ = windows.UnlockFileEx(h, 0, 1, 0, &pendingOL2)
		return err
	}
	return nil
}

// Unlock releases the exclusive lock acquired by LockExclusive.
func Unlock(f *os.File) error {
	h := windows.Handle(f.Fd())
	sharedOL := windows.Overlapped{Offset: sqliteSharedFirst}
	err1 := windows.UnlockFileEx(h, 0, sqliteSharedSize, 0, &sharedOL)
	pendingOL := windows.Overlapped{Offset: sqlitePendingByte}
	err2 := windows.UnlockFileEx(h, 0, 1, 0, &pendingOL)
	if err1 != nil {
		return err1
	}
	return err2
}
