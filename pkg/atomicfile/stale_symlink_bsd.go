//go:build freebsd || dragonfly

package atomicfile

import "syscall"

// symlinkErrs are the errors of an open with O_NOFOLLOW of a symlink:
// FreeBSD and DragonFly give EMLINK (ELOOP: a loop in the path).
var symlinkErrs = []error{syscall.EMLINK, syscall.ELOOP}
