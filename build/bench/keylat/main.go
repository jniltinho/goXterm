// Command keylat timestamps input as it reaches a process inside the terminal under test (the bench
// harness compares it with when xdotool sent each key):
//
//	keylat input N OUT   read N bytes from stdin (put the tty in raw mode first), one ns time per line
//	keylat sigint OUT    wait for SIGINT (Ctrl+C) and write its arrival time
package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: keylat input N OUT | keylat sigint OUT")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "input":
		n, _ := strconv.Atoi(os.Args[2])
		var lines []string
		b := make([]byte, 1)
		for len(lines) < n {
			if _, err := os.Stdin.Read(b); err != nil {
				break
			}
			lines = append(lines, strconv.FormatInt(time.Now().UnixNano(), 10))
		}
		_ = os.WriteFile(os.Args[3], []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	case "sigint":
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT)
		fmt.Println("waiting for Ctrl+C")
		<-ch
		_ = os.WriteFile(os.Args[2], []byte(strconv.FormatInt(time.Now().UnixNano(), 10)+"\n"), 0o644)
	}
}
