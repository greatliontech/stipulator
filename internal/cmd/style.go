package cmd

import (
	"fmt"
	"io"
	"os"
)

// styler tints human-facing text for one output stream: enabled only when
// that stream itself is a character device and neither NO_COLOR nor
// TERM=dumb disables terminal color. The wire surfaces (MCP, protos) carry
// the same data uncolored; these verbs are for people, and a redirected
// stream never receives escapes while a sibling terminal stream keeps its
// color independently.
type styler struct {
	on bool
}

type statWriter interface {
	io.Writer
	Stat() (os.FileInfo, error)
}

func styleFor(w io.Writer) styler {
	if d, ok := w.(dimWriter); ok {
		w = d.w
	}
	sw, ok := w.(statWriter)
	if !ok {
		return styler{}
	}
	return styleStat(sw, os.Getenv)
}

func styleStat(sw statWriter, getenv func(string) string) styler {
	if sw == nil || getenv("NO_COLOR") != "" || getenv("TERM") == "dumb" {
		return styler{}
	}
	fi, err := sw.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return styler{}
	}
	return styler{on: true}
}

func outStyle() styler { return styleFor(os.Stdout) }
func errStyle() styler { return styleFor(os.Stderr) }

func (st styler) tint(code, s string) string {
	if !st.on {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (st styler) green(s string) string  { return st.tint("32", s) }
func (st styler) red(s string) string    { return st.tint("31", s) }
func (st styler) yellow(s string) string { return st.tint("33", s) }
func (st styler) dim(s string) string    { return st.tint("2", s) }
func (st styler) bold(s string) string   { return st.tint("1", s) }

// num colors a count only when it is bad news.
func (st styler) num(n int, color func(string) string) string {
	s := fmt.Sprint(n)
	if n == 0 {
		return s
	}
	return color(s)
}
