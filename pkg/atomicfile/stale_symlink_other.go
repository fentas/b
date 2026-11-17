//go:build linux || darwin || openbsd

package atomicfile

import "syscall"

// symlinkErrs are the errors of an open with O_NOFOLLOW of a symlink:
// Linux, macOS and OpenBSD give ELOOP.
var symlinkErrs = []error{syscall.ELOOP}
