//go:build unix

package shell

import (
	"errors"
	"os/exec"
	"syscall"
)

// setProcessGroup puts the command into its own process group, so
// a cancellation can take down the whole tree: a command that
// backgrounds a child and leaves it inheriting the output pipes
// would otherwise survive the kill of its parent, and the pipes
// would stay open long past the timeout.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTree kills the command's whole process group. A group that
// is already gone is success, not an error: the wait would
// otherwise report the kill's ESRCH instead of the timeout. A
// command that never started has no group and nothing to kill.
func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
