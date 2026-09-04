package cmd

import (
	"fmt"
	"os"
)

// Human-facing color: on only when both streams the verbs tint —
// stdout and stderr — are terminals and NO_COLOR is unset. The wire
// surfaces (MCP, protos) carry the same data uncolored; these verbs are
// for people, and a redirected stream must never receive escapes.
var colorOn = func() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	for _, f := range []*os.File{os.Stdout, os.Stderr} {
		fi, err := f.Stat()
		if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
			return false
		}
	}
	return true
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
