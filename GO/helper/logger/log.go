package logger

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// ANSI escape codes for colors
const (
	Red     = "\033[91m"
	Green   = "\033[92m"
	Yellow  = "\033[93m"
	Blue    = "\033[94m"
	Magenta = "\033[95m"
	Cyan    = "\033[96m"
	White   = "\033[97m"
	Reset   = "\033[0m" // Reset color
)

func Log(args ...any) {
	printColor(Magenta, args)
}

func Error(args ...any) {
	printColor(Red, args)
}

func Success(args ...any) {
	printColor(Green, args)
}

func Warn(args ...any) {
	printColor(Yellow, args)
}

func Info(args ...any) {
	printColor(Blue, args)
}

func Logo() {
	banner := `
   __     __  _                                  
   \ \   / / | |                                 
    \ \_/ /__| | _ ___  _ __   __ _  __ _        
     \   / _ \ |/ / _ \| '_ \ /  ' |/  ' |       
      | |  __/   < (_) | | | |  () |  () |       
      |_|\___|_|\_\___/|_| |_|\__  |\__,_|       
                              \____|   `

	Warn(banner, "no-line-break")
	Info("SERVER", "no-line-break")
	Warn(".", "no-line-break")
	Info("GO \n")

}

func LogoLarge() {
	Success(`	___      ___    __ `)
	Success(`	\  \    /  /   |  |`)
	Success(`	 \  \  /  /    |  |`)
	Success(`	  \  \/  /____ |  | ___ ____  __ ___   ___ __  ___ __ `)
	Success(`	   \    /  _  \|  |/  /  _  \|  '_  \ /  _'  |/  _'  |`)
	Success(`	    |  |  (_)  |  '  /  / \  \  | |  |  / \  |  / \  |`)
	Success(`	    |  |   ___/|     \  \_/  /  | |  |  \_/  |  \_/  |`)
	Success(`	    |__|\_____/|__|\__\_____/|__| |__|\____  |\____,_|`)
	Success(`	                                      ____/  |   `, "no-line-break")
	Info("SERVER", "no-line-break")
	Success(".", "no-line-break")
	Info("GO")
	Success(`	                                      \_____/         `)
}

func LogoHash() {
	banner := `
                                         
   ##     ##     ##                             
    ##   ##      ##         
     ## ## ####  ##  ##  ###  ####   ###   ####
      ### ##  ## ####   ## ## ## ## ## ## ## ##
      ###  ####  ##  ##  ###  ## ##  ####  ####
	                                  ##    `

	Warn(banner, "no-line-break")
	Info("SERVER", "no-line-break")
	Warn(".", "no-line-break")
	Info("GO \n")
}

func printColor(color string, args []any) {
	count := len(args)
	hasFormat := false
	formatString := ""
	otherArgs := []any{}
	noLineBreak := slices.Contains(args, "no-line-break")
	skipPos := slices.Index(args, "no-line-break")

	if count > 1 && !noLineBreak {
		if s, ok := args[0].(string); ok && strings.Contains(s, "%") {
			hasFormat = true
		}
	}

	for argNum, arg := range args {
		if noLineBreak && argNum == skipPos {
			continue
		}

		if argNum == 0 {
			if hasFormat {
				if v, ok := arg.(string); ok {
					formatString = v
				}
			} else {
				fmt.Print(color, getString(arg), Reset)
				if count > 1 && !noLineBreak {
					fmt.Print(": ")
				}
			}
		} else {
			if hasFormat {
				if err, ok := arg.(error); ok {
					otherArgs = append(otherArgs, err.Error())
				} else {
					otherArgs = append(otherArgs, arg)
				}
			} else {
				fmt.Print(getString(arg) + " ")
			}
		}
	}

	if hasFormat {
		fmt.Print(color, getString(string(fmt.Appendf([]byte{}, formatString, otherArgs...))), Reset)
	}

	if !noLineBreak {
		fmt.Print("\n")
	}
}

func getString(data interface{}) string {
	var str string

	if v, ok := data.(string); ok {
		str = v
	} else {
		str = toJson(data)
	}

	return str
}

func toJson(data interface{}) string {
	jsonData, _ := json.MarshalIndent(data, "", " ")

	return string(jsonData)
}

func WriteToFile(filename string, data string) {
	logDir := GetPath("./logs")
	fmt.Println("Log file path:", logDir)
	if err := os.MkdirAll(logDir, 0644); err != nil {
		fmt.Println("Error creating logs directory:", err)
		return
	}

	currentDate := time.Now().Format("2006-01-02")
	logFilename := filepath.Join(logDir, fmt.Sprintf("%s_%s", currentDate, filename))
	// fmt.Println("Log file path:", logFilename)

	file, err := os.OpenFile(logFilename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("Error opening file:", err)
		return
	}
	defer file.Close()

	if _, err := file.WriteString(data + "\n"); err != nil {
		fmt.Println("Error writing to file:", err)
	}
}

func LoadFile(filename string) string {
	file, err := os.Open(filename)
	if err != nil {
		fmt.Println(err)
		return ""
	}
	defer file.Close()

	bytes, err := io.ReadAll(file)
	if err != nil {
		fmt.Println(err)
		return ""
	}

	return string(bytes)
}

func GetPath(relativePath string) string {
	// 1. Get the path of the executable
	ex, err := os.Executable()
	if err != nil {
		log.Fatalf("Error getting executable path: %v", err)
	}
	exPath := filepath.Dir(ex)
	absolutePath := filepath.Join(exPath, relativePath)

	if FileExists(absolutePath) {
		return absolutePath
	}

	if FileExists(relativePath) {
		absPath, err := filepath.Abs(relativePath)
		if err != nil {
			return relativePath
		}
		return absPath
	}

	return absolutePath
}

// FileExists checks if a file exists
func FileExists(filename string) bool {
	_, err := os.Stat(filename)

	if err != nil {
		// console.Error("FileExists", err)
	}

	return !os.IsNotExist(err)
}
