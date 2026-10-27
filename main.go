package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "-h" || os.Args[1] == "--help") {
		fmt.Println("usage: hunklog [repo]")
		fmt.Println()
		fmt.Println("Browse commits (newest first) and open one in hunk.")
		fmt.Println("The patch is git show <hash> -U99999: every file, shown in full.")
		return
	}
	if len(os.Args) > 2 {
		fmt.Fprintln(os.Stderr, "usage: hunklog [repo]")
		os.Exit(2)
	}
	path := "."
	if len(os.Args) == 2 {
		path = os.Args[1]
	}
	p := tea.NewProgram(model{path: path}, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
