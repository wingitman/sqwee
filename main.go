package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	// Blank import forces every built-in driver's init() to run so they
	// self-register with the driver registry. Third-party drivers added to
	// the internal/driver package register the same way. See SPEC.md.
	_ "main.go/internal/driver"
)

func main() {
	m, err := NewModel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error initialising sqwee:", err)
		os.Exit(1)
	}

	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "Error running sqwee:", err)
		os.Exit(1)
	}
}
