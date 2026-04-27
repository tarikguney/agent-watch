//go:build windows

package process

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

var (
	ntdll                     = syscall.NewLazyDLL("ntdll.dll")
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procNtQueryInformationProcess = ntdll.NewProc("NtQueryInformationProcess")
	procReadProcessMemory     = kernel32.NewProc("ReadProcessMemory")
)

const (
	processQueryInformation = 0x0400
	processVMRead           = 0x0010
)

type processBasicInformation struct {
	Reserved1       uintptr
	PebBaseAddress  uintptr
	Reserved2       [2]uintptr
	UniqueProcessId uintptr
	Reserved3       uintptr
}

// GetProcessCwd reads the current working directory of a process on Windows
// by inspecting the Process Environment Block (PEB) via NtQueryInformationProcess.
// Note: PEB offsets are for x64 only (ProcessParameters at 0x20, CurrentDirectory at 0x38).
func GetProcessCwd(pid int) (string, error) {
	handle, err := syscall.OpenProcess(processQueryInformation|processVMRead, false, uint32(pid))
	if err != nil {
		return "", fmt.Errorf("OpenProcess(%d): %w", pid, err)
	}
	defer syscall.CloseHandle(handle)

	// Get PEB address via NtQueryInformationProcess
	var pbi processBasicInformation
	var retLen uint32
	r, _, _ := procNtQueryInformationProcess.Call(
		uintptr(handle),
		0, // ProcessBasicInformation
		uintptr(unsafe.Pointer(&pbi)),
		uintptr(unsafe.Sizeof(pbi)),
		uintptr(unsafe.Pointer(&retLen)),
	)
	if r != 0 {
		return "", fmt.Errorf("NtQueryInformationProcess: status 0x%x", r)
	}

	// Read ProcessParameters pointer from PEB (offset 0x20 on x64)
	var processParams uintptr
	if err := readProcMem(handle, pbi.PebBaseAddress+0x20, unsafe.Pointer(&processParams), unsafe.Sizeof(processParams)); err != nil {
		return "", fmt.Errorf("read PEB.ProcessParameters: %w", err)
	}

	// Read CurrentDirectory.DosPath UNICODE_STRING at offset 0x38 in RTL_USER_PROCESS_PARAMETERS
	// UNICODE_STRING layout on x64: Length(2) + MaxLength(2) + pad(4) + Buffer(8) = 16 bytes
	var length uint16
	if err := readProcMem(handle, processParams+0x38, unsafe.Pointer(&length), unsafe.Sizeof(length)); err != nil {
		return "", fmt.Errorf("read DosPath.Length: %w", err)
	}
	if length == 0 {
		return "", fmt.Errorf("empty CWD for pid %d", pid)
	}

	var buffer uintptr
	if err := readProcMem(handle, processParams+0x38+8, unsafe.Pointer(&buffer), unsafe.Sizeof(buffer)); err != nil {
		return "", fmt.Errorf("read DosPath.Buffer: %w", err)
	}

	// Read the UTF-16 path string
	cwdBuf := make([]uint16, length/2)
	if err := readProcMem(handle, buffer, unsafe.Pointer(&cwdBuf[0]), uintptr(length)); err != nil {
		return "", fmt.Errorf("read CWD string: %w", err)
	}

	cwd := syscall.UTF16ToString(cwdBuf)
	return strings.TrimRight(cwd, `\`), nil
}

func readProcMem(handle syscall.Handle, addr uintptr, buf unsafe.Pointer, size uintptr) error {
	var bytesRead uintptr
	r, _, err := procReadProcessMemory.Call(
		uintptr(handle),
		addr,
		uintptr(buf),
		size,
		uintptr(unsafe.Pointer(&bytesRead)),
	)
	if r == 0 {
		return err
	}
	return nil
}
