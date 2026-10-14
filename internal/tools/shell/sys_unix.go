//go:build unix

package shell

import (
	"errors"
	"syscall"
)

// procGroupAttr puts the command into its own process group, so a
// cancellation can take down the whole tree: a command that
// backgrounds a child and leaves it inheriting the output pipes
// would otherwise survive the kill of its parent, and the pipes
// would stay open long past the timeout.
func procGroupAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// killGroup kills the process group of a cancelled command. A
// group that is already gone is success, not an error: the wait
// would otherwise report the kill's ESRCH instead of the timeout.
func killGroup(pid int) error {
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
