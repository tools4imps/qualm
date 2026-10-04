// Command qualm fails a pull request when Jev says a reviewer would push back on a changed file.
package main

import (
	"fmt"
	"os"

	"github.com/tools4imps/qualm/internal/cli"
)

func main() {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "qualm: %v\n", err)
		os.Exit(2)
	}
	os.Exit(cli.Run(os.Args[1:], cli.Env{
		Dir:    dir,
		Getenv: os.Getenv,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}))
}
