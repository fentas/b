//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly || windows)

package atomicfile

import "os"

// Lockable is false: Create takes no lock on this platform (stale_flock.go).
const Lockable = false

// lockTemp takes no lock, and reports true: this platform has no flock in
// the syscall package, and removeIfStale removes nothing here.
func lockTemp(*os.File) bool { return true }

// removeIfStale never removes a file on this platform: without a lock, b
// cannot find out whether a process still uses the temp file, and the
// system removes a file that is open.
func removeIfStale(string) (bool, error) { return false, nil }
