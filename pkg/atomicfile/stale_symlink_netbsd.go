//go:build netbsd

package atomicfile

import "syscall"

// symlinkErrs are the errors of an open with O_NOFOLLOW of a symlink:
// NetBSD gives EFTYPE (ELOOP: a loop in the path).
var symlinkErrs = []error{syscall.EFTYPE, syscall.ELOOP}
