// Command stipulator compiles and verifies a specification corpus.
package main

import (
	"fmt"
	"os"

	"github.com/greatliontech/stipulator/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "stipulator:", err)
		os.Exit(2)
	}
}
