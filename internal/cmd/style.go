package cmd

import (
	"fmt"
	"os"
)

// Human-facing color: on only for a terminal stdout without NO_COLOR. The
// wire surfaces (MCP, protos) carry the same data uncolored; these verbs
// are for people.
var colorOn = func() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}()

func tint(code, s string) string {
	if !colorOn {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func green(s string) string  { return tint("32", s) }
func red(s string) string    { return tint("31", s) }
func yellow(s string) string { return tint("33", s) }
func dim(s string) string    { return tint("2", s) }
func bold(s string) string   { return tint("1", s) }

// num colors a count only when it is bad news.
func num(n int, color func(string) string) string {
	s := fmt.Sprint(n)
	if n == 0 {
		return s
	}
	return color(s)
}
