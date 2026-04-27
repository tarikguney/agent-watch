//go:build !windows

package process

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// getParentPID returns the parent process ID for a given PID on Unix.
func getParentPID(pid int) int {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "PPid:") {
			ppid, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "PPid:")))
			if err != nil {
				return 0
			}
			return ppid
		}
	}
	return 0
}

// buildParentPIDMap is a no-op on Unix; /proc reads are O(1) per lookup so
// callers fall through to getParentPID. Returning nil signals that behavior
// to walkParentPIDs.
func buildParentPIDMap() map[int]int { return nil }
