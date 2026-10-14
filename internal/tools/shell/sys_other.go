//go:build !unix

package shell

import "syscall"

// procGroupAttr has nothing to set on this platform; the timeout
// falls back to WaitDelay alone, which still bounds the run.
func procGroupAttr() *syscall.SysProcAttr { return nil }

// killGroup cannot act on this platform; the direct child kill
// from exec.CommandContext and WaitDelay carry the cancellation.
func killGroup(int) error { return nil }
