//go:build windows

package runner

import (
	"os/exec"
	"time"
)

func configureProcGroup(_ *exec.Cmd) {}

func killProcGroup(cmd *exec.Cmd, done <-chan error) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}
