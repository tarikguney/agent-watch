//go:build !windows

package process

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// GetProcessCwd reads the current working directory of a process.
// On Linux, reads /proc/<pid>/cwd. On macOS, uses lsof.
func GetProcessCwd(pid int) (string, error) {
	// Linux: /proc/<pid>/cwd symlink
	link := fmt.Sprintf("/proc/%d/cwd", pid)
	target, err := os.Readlink(link)
	if err == nil {
		return target, nil
	}

	// macOS: use lsof as fallback
	if runtime.GOOS == "darwin" {
		out, lsofErr := exec.Command("lsof", "-a", "-d", "cwd", "-Fn", "-p", fmt.Sprintf("%d", pid)).Output()
		if lsofErr == nil {
			for _, line := range strings.Split(string(out), "\n") {
				if strings.HasPrefix(line, "n") {
					return line[1:], nil
				}
			}
		}
		return "", fmt.Errorf("lsof failed for pid %d: %w", pid, lsofErr)
	}

	// Linux but Readlink failed — return the original error
	return "", fmt.Errorf("readlink /proc/%d/cwd: %w", pid, err)
}
