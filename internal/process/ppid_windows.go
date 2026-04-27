//go:build windows

package process

import (
	"syscall"
	"unsafe"
)

var procCreateToolhelp32Snapshot = kernel32.NewProc("CreateToolhelp32Snapshot")
var procProcess32First = kernel32.NewProc("Process32FirstW")
var procProcess32Next = kernel32.NewProc("Process32NextW")

const th32csSnapProcess = 0x00000002

type processEntry32 struct {
	Size              uint32
	CntUsage          uint32
	ProcessID         uint32
	DefaultHeapID     uintptr
	ModuleID          uint32
	CntThreads        uint32
	ParentProcessID   uint32
	PriClassBase      int32
	Flags             uint32
	ExeFile           [260]uint16
}

// getParentPID returns the parent process ID for a given PID on Windows.
// Prefer buildParentPIDMap() when resolving multiple PIDs — this function
// takes a full process-table snapshot per call.
func getParentPID(pid int) int {
	m := buildParentPIDMap()
	return m[pid]
}

// buildParentPIDMap returns a pid->ppid map using a single system-wide
// snapshot. Callers that look up multiple ancestors should build this once
// and reuse it instead of calling getParentPID repeatedly.
func buildParentPIDMap() map[int]int {
	result := make(map[int]int)
	handle, _, _ := procCreateToolhelp32Snapshot.Call(th32csSnapProcess, 0)
	if handle == ^uintptr(0) {
		return result
	}
	defer syscall.CloseHandle(syscall.Handle(handle))

	var entry processEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	r, _, _ := procProcess32First.Call(handle, uintptr(unsafe.Pointer(&entry)))
	if r == 0 {
		return result
	}
	for {
		result[int(entry.ProcessID)] = int(entry.ParentProcessID)
		entry.Size = uint32(unsafe.Sizeof(entry))
		r, _, _ = procProcess32Next.Call(handle, uintptr(unsafe.Pointer(&entry)))
		if r == 0 {
			break
		}
	}
	return result
}
