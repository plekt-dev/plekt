// plekt-updater is a standalone helper binary that replaces the core executable
// after the main process has been killed. It is spawned by the core's updater
// package and is expected to live next to the main binary.
//
// Usage:
//
//	plekt-updater --pid 1234 --target /path/to/plekt-core --new /path/to/plekt-core.new --cwd /work/dir [--args "arg1\x00arg2"]
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

func main() {
	pid := flag.Int("pid", 0, "PID of the process to kill")
	target := flag.String("target", "", "path to the current executable")
	newBin := flag.String("new", "", "path to the new executable (.new)")
	cwd := flag.String("cwd", "", "working directory for the new process")
	args := flag.String("args", "", "original args (null-separated)")
	flag.Parse()

	if *pid == 0 || *target == "" || *newBin == "" {
		flag.Usage()
		os.Exit(1)
	}

	log.SetPrefix("plekt-updater: ")
	log.SetFlags(log.Ltime)

	// 1. Kill the old process.
	log.Printf("killing process %d", *pid)
	if p, err := os.FindProcess(*pid); err == nil {
		_ = p.Kill()
	}

	// 2. Wait for the process to die (up to 30s).
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(*pid) {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if processAlive(*pid) {
		log.Fatalf("process %d did not exit in time", *pid)
	}
	log.Println("old process exited")

	// Small delay so the OS fully releases file handles.
	time.Sleep(500 * time.Millisecond)

	// 3. Rename: target → target.old
	oldPath := *target + ".old"
	_ = os.Remove(oldPath)
	if err := os.Rename(*target, oldPath); err != nil {
		log.Printf("rename old: %v (continuing)", err)
	}

	// 4. Rename: .new → target (copy as fallback)
	if err := os.Rename(*newBin, *target); err != nil {
		log.Printf("rename new failed: %v, trying copy", err)
		if err := copyFile(*newBin, *target); err != nil {
			log.Printf("copy also failed: %v: restoring old binary", err)
			_ = os.Rename(oldPath, *target)
			os.Exit(1)
		}
		_ = os.Remove(*newBin)
	}
	_ = os.Chmod(*target, 0o755)

	// 5. Start the new binary.
	var originalArgs []string
	if *args != "" {
		originalArgs = strings.Split(*args, "\x00")
	}

	log.Printf("starting %s %v (cwd=%s)", *target, originalArgs, *cwd)
	cmd := exec.Command(*target, originalArgs...)
	if *cwd != "" {
		cmd.Dir = *cwd
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Start(); err != nil {
		log.Fatalf("failed to start new binary: %v", err)
	}

	log.Printf("done: new process PID %d", cmd.Process.Pid)
}

// processAlive checks if a process with the given PID exists.
func processAlive(pid int) bool {
	if runtime.GOOS == "windows" {
		// On Windows, os.FindProcess always succeeds. Use tasklist.
		out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH").Output()
		if err != nil {
			return false
		}
		return strings.Contains(string(out), fmt.Sprintf("%d", pid))
	}
	// Unix: signal 0 checks existence.
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(os.Signal(nil)) == nil
}

// copyFile copies src to dst as a fallback when rename fails (cross-device).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
