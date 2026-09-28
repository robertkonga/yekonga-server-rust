//go:build linux
// +build linux

package idle_time

import (
	"fmt"
	"os/exec"
	"os/user"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// // getIdleTime is the internal function for Linux.
// func GetIdleTime() time.Duration {
// 	// NOTE: Requires the 'xprintidle' utility to be installed.
// 	out, err := exec.Command("xprintidle").Output()
// 	if err != nil {
// 		fmt.Printf("Linux: **xprintidle** utility failed (is it installed/running X?): %v\n", err)
// 		return 0
// 	}

// 	// Output is in milliseconds
// 	msStr := strings.TrimSpace(string(out))
// 	milliseconds, err := strconv.ParseInt(msStr, 10, 64)
// 	if err != nil {
// 		fmt.Printf("Linux output parsing failed: %v\n", err)
// 		return 0
// 	}

// 	return time.Duration(milliseconds) * time.Millisecond
// }

// // getIdleTime is the internal function for Linux.
// func GetIdleTime() time.Duration {
// 	// NOTE: This uses the 'xssstate' utility, which is a common alternative to 'xprintidle'.
// 	// The '-i' flag returns the idle time in milliseconds.
// 	out, err := exec.Command("xssstate", "-i").Output()
// 	if err != nil {
// 		// Try using a more descriptive error message here
// 		fmt.Printf("Linux: **xssstate** utility failed (is it installed/running X?): %v\n", err)
// 		return 0
// 	}

// 	// Output is in milliseconds
// 	msStr := strings.TrimSpace(string(out))
// 	milliseconds, err := strconv.ParseInt(msStr, 10, 64)
// 	if err != nil {
// 		fmt.Printf("Linux output parsing failed: %v\n", err)
// 		return 0
// 	}

// 	return time.Duration(milliseconds) * time.Millisecond
// }

// getIdleTime is the internal function for Linux, using the 'w' command.
func GetIdleTime() time.Duration {
	// 1. Get the current logged-in user's name
	currentUser, err := user.Current()
	if err != nil {
		fmt.Printf("Linux: Failed to get current user: %v\n", err)
		return 0
	}
	username := currentUser.Username

	// 2. Execute 'w' command and look for the current user's entry.
	// We use 'w -h' to suppress the header.
	out, err := exec.Command("w", "-h").Output()
	if err != nil {
		fmt.Printf("Linux: 'w' command failed (Is /usr/bin/w missing?): %v\n", err)
		return 0
	}

	fmt.Print(string(out)) // Debug: print the output of 'w' command

	lines := strings.Split(string(out), "\n")
	var idleDuration time.Duration = 0

	// 3. Regex to match the idle time (usually the 4th field after TTY, TIME, etc.)
	// The idle time column can be like "5s", "1:30m", "1day", or just a space/hyphen for active.
	// It's usually the 4th field after the TTY name.
	// Example line: "user pts/0 :0 13:37 1:00m 5s /usr/bin/bash"
	// We will look for time format in the fourth non-empty field after the initial user/tty/login fields.

	// Pre-compile time patterns
	timePattern := regexp.MustCompile(`(\d+)(s|m|h|days)`)

	for _, line := range lines {
		if strings.HasPrefix(line, username) {
			fields := strings.Fields(line)
			if len(fields) < 5 { // Expect at least 5 fields (user, tty, from, login, idle)
				continue
			}

			idleStr := fields[4] // This is typically the idle time field

			// If idle field is a dash or short (active), it means 0 or very low idle time.
			if idleStr == "-" || idleStr == " " || strings.HasPrefix(idleStr, "0") {
				return 0
			}

			// Attempt to parse the duration string
			if idleStr == "JCPU" || idleStr == "PCPU" || idleStr == "WHAT" {
				// Skip header lines that 'w -h' might miss or if we hit the wrong column
				continue
			}

			// Handle "days" format (e.g., "1day", "2days")
			if strings.Contains(idleStr, "day") {
				// w shows full days, e.g., "1day"
				// We can't parse this easily as a go duration, so we assume 24h per day.
				daysStr := strings.TrimSuffix(idleStr, "day")
				daysStr = strings.TrimSuffix(daysStr, "s") // handle "days"
				if days, err := strconv.Atoi(daysStr); err == nil {
					idleDuration = time.Duration(days) * 24 * time.Hour
					return idleDuration
				}
			}

			// Handle time format (e.g., "5s", "1:30m", "20min")
			// 'w' uses formats like "1m" (1 minute) or "1:30" (1 hour 30 min)
			if strings.Contains(idleStr, ":") {
				// Format is H:MM (e.g., 1:30 for 1 hour 30 mins)
				parts := strings.Split(idleStr, ":")
				if len(parts) == 2 {
					hours, _ := strconv.Atoi(parts[0])
					minutes, _ := strconv.Atoi(parts[1])
					idleDuration = time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute
					return idleDuration
				}
			}

			// Format is Xm, Xs, Xh (e.g., 5s, 1m, 2h)
			matches := timePattern.FindStringSubmatch(idleStr)
			if len(matches) == 3 {
				value, _ := strconv.Atoi(matches[1])
				unit := matches[2]

				switch unit {
				case "s":
					idleDuration = time.Duration(value) * time.Second
				case "m":
					idleDuration = time.Duration(value) * time.Minute
				case "h":
					idleDuration = time.Duration(value) * time.Hour
				case "days":
					idleDuration = time.Duration(value) * 24 * time.Hour
				}
				return idleDuration
			}

			// Fallback for very small seconds, w just shows the number like "5"
			if seconds, err := strconv.Atoi(idleStr); err == nil && seconds < 60 {
				idleDuration = time.Duration(seconds) * time.Second
				return idleDuration
			}
		}
	}

	// If no active session found or parsing failed, assume active (0 idle)
	return 0
}
