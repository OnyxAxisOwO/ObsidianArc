// A WASI command that stands in for an interpreter in the tests of
// Backend.Run: it is told what to do by its first argument, the way an
// interpreter is told which file to run.
package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

var hog [][]byte

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "no mode")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "echo":
		data, _ := io.ReadAll(os.Stdin)
		os.Stdout.Write(data)
	case "args":
		fmt.Print(strings.Join(os.Args[2:], ","))
	case "exit":
		code, _ := strconv.Atoi(os.Args[2])
		fmt.Fprint(os.Stderr, "leaving")
		os.Exit(code)
	case "file":
		data, err := os.ReadFile(os.Args[2])
		if err != nil {
			fmt.Fprint(os.Stderr, err)
			os.Exit(1)
		}
		os.Stdout.Write(data)
	case "write":
		if err := os.WriteFile(os.Args[2], []byte("x"), 0o644); err != nil {
			fmt.Print("denied")
			return
		}
		fmt.Print("written")
	case "hostfs":
		if _, err := os.ReadDir("/"); err != nil {
			fmt.Print("denied")
			return
		}
		entries, _ := os.ReadDir("/")
		for _, e := range entries {
			fmt.Println(e.Name())
		}
	case "env":
		fmt.Print(len(os.Environ()))
	case "flood":
		line := strings.Repeat("y", 1023) + "\n"
		for i := 0; i < 4096; i++ {
			os.Stdout.WriteString(line)
		}
	case "spin":
		for {
		}
	case "sleep":
		ms, _ := strconv.Atoi(os.Args[2])
		start := time.Now()
		time.Sleep(time.Duration(ms) * time.Millisecond)
		fmt.Print(time.Since(start).Milliseconds())
	case "memhog":
		for i := 0; i < 300; i++ {
			block := make([]byte, 1<<20)
			for j := range block {
				block[j] = byte(j)
			}
			hog = append(hog, block)
		}
		fmt.Print(len(hog))
	default:
		fmt.Fprintln(os.Stderr, "unknown mode", os.Args[1])
		os.Exit(2)
	}
}
