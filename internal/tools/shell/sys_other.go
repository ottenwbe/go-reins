//go:build !unix

package shell

import "os/exec"

// setProcessGroup has nothing to set on this platform; the
// timeout falls back to killing the direct child, with WaitDelay
// bounding the run.
func setProcessGroup(*exec.Cmd) {}

// killTree falls back to killing the direct child - the same
// behavior exec.CommandContext would have had before the Cancel
// override. Background children cannot be reached without process
// groups here; WaitDelay still bounds the run. A command that
// never started has nothing to kill.
func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
