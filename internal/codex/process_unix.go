//go:build darwin || linux

package codex

import (
	"os"
	"os/exec"
	"syscall"
)

// The npm launcher spawns a native child. Kill the process group on timeout so
// neither launcher nor native model work can outlive the request.
func isolateProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
}
