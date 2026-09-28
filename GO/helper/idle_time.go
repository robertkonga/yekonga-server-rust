package helper

import (
	"time"

	"github.com/robertkonga/yekonga-server-go/helper/idle_time"
)

// The central function that calls the OS-specific function
func GetCrossPlatformIdleTime() time.Duration {
	return idle_time.GetIdleTime()
}
