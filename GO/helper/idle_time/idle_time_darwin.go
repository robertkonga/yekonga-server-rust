//go:build darwin
// +build darwin

package idle_time

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// getIdleTime is the internal function for macOS.
func GetIdleTime() time.Duration {
	// Query IOHIDSystem for HIDIdleTime (in nanoseconds)
	cmd := "ioreg -c IOHIDSystem | awk '/HIDIdleTime/ {print $NF}'"
	out, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		fmt.Printf("macOS command failed: %v\n", err)
		return 0
	}

	nanoStr := strings.TrimSpace(string(out))
	nanoseconds, err := strconv.ParseInt(nanoStr, 10, 64)
	if err != nil {
		fmt.Printf("macOS output parsing failed: %v\n", err)
		return 0
	}

	return time.Duration(nanoseconds) * time.Nanosecond
}
