//go:build windows
// +build windows

package idle_time

import (
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Define the necessary Windows structures
type lastInputInfo struct {
	cbSize uint32
	dwTime uint32
}

var (
	user32 = windows.NewLazyDLL("user32.dll")
	// Procedure for GetLastInputInfo
	getLastInputInfo = user32.NewProc("GetLastInputInfo")

	// 👇 FIX: Define GetTickCount as a procedure from kernel32.dll
	kernel32         = windows.NewLazyDLL("kernel32.dll")
	getTickCountProc = kernel32.NewProc("GetTickCount")
)

// Helper function to call the GetTickCount WinAPI
func getWinAPITickCount() uint32 {
	// The WinAPI function returns the tick count as a single return value.
	ret, _, _ := getTickCountProc.Call()
	return uint32(ret)
}

// getIdleTime is the internal function for Windows.
func GetIdleTime() time.Duration {
	var info lastInputInfo
	info.cbSize = uint32(unsafe.Sizeof(info))

	ret, _, err := getLastInputInfo.Call(uintptr(unsafe.Pointer(&info)))
	if ret == 0 {
		fmt.Printf("Windows API call failed: %v\n", err)
		return 0
	}

	// Calculate idle time (current tick count - last input tick count)
	// 👇 FIX: Call the helper function
	idleMs := getWinAPITickCount() - info.dwTime
	return time.Duration(idleMs) * time.Millisecond
}
