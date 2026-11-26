//go:build !windows

package runner

import (
	"os/exec"
	"syscall"
	"time"
)

func configureProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcGroup(cmd *exec.Cmd, done <-chan error) {
	pgid := cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	exited := false
	select {
	case <-done:
		exited = true
	case <-time.After(2 * time.Second):
	}
	// Always send SIGKILL to the process group so descendants that ignore
	// SIGTERM cannot survive after the direct child exits.
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	if !exited {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
}
