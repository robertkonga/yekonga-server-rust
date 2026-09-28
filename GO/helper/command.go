package helper

import (
	"bufio"
	"fmt"
	"log"
	"os/exec"
)

func Command(name string, arg ...string) interface{} {
	// The command and arguments are passed separately to exec.Command
	// Note: The program running this Go code must have sudo rights
	// (e.g., run the Go binary with `sudo`) or be configured for passwordless sudo
	cmd := exec.Command(name, arg...)

	// 1. Create a pipe to read the standard output of the command
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatalf("Error creating StdoutPipe: %v", err)
	}

	// 2. Start the command
	if err := cmd.Start(); err != nil {
		log.Fatalf("Error starting command: %v", err)
	}

	// 3. Use a bufio.Scanner in a goroutine to read the output line by line
	// This is crucial for handling the continuous stream from 'journalctl -f'
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		// Display the line as soon as it is read
		fmt.Println(scanner.Text())
	}

	// Check for scanner errors (e.g., if the pipe closed unexpectedly)
	if err := scanner.Err(); err != nil {
		log.Printf("Error reading stdout: %v", err)
	}

	// 4. Wait for the command to finish (it won't normally for journalctl -f
	// unless the Go program is killed with Ctrl+C, which also kills the child process).
	if err := cmd.Wait(); err != nil {
		// When killed by Ctrl+C, the error is usually "signal: interrupt"
		// or "exit status 1", which is expected behavior.
		log.Printf("Command finished with error: %v", err)
	}

	return nil
}
