package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

var stdin = bufio.NewReader(os.Stdin)

func interactive() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// choose prints numbered options and returns the picked index (def on empty input).
func choose(title string, options []string, def int) int {
	fmt.Fprintln(os.Stderr, title)
	for i, o := range options {
		mark := " "
		if i == def {
			mark = "›"
		}
		fmt.Fprintf(os.Stderr, " %s %d) %s\n", mark, i+1, o)
	}
	for {
		fmt.Fprintf(os.Stderr, "Chọn [%d]: ", def+1)
		line, _ := stdin.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return def
		}
		if n, err := strconv.Atoi(line); err == nil && n >= 1 && n <= len(options) {
			return n - 1
		}
	}
}

// confirm asks a yes/no question.
func confirm(q string, def bool) bool {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	fmt.Fprintf(os.Stderr, "%s [%s] ", q, hint)
	line, _ := stdin.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return def
	case "y", "yes", "c", "co", "có":
		return true
	}
	return false
}
