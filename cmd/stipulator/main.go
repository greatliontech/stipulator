// Command stipulator compiles and verifies a specification corpus.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/greatliontech/stipulator/internal/cmd"
)

func main() {
	if err := cmd.Execute(context.Background()); err != nil {
		var status cmd.ExitStatus
		if errors.As(err, &status) {
			// A verdict already rendered: the code alone.
			os.Exit(status.Code)
		}
		fmt.Fprintln(os.Stderr, "stipulator:", err)
		os.Exit(2)
	}
}
