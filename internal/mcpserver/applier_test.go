package mcpserver

import (
	"io/fs"
	"testing/fstest"

	"github.com/greatliontech/stipulator/internal/recordapply"
)

// memoryApplier is the applier over an in-memory tree: every stage
// lands in writes and the tree on commit, so the server reads what it
// wrote — the read-after-write flows (pin to quiescence, re-declare
// over an update) depend on it. writes records a deletion as nil.
func memoryApplier(mem fstest.MapFS, writes map[string][]byte) *recordapply.Applier {
	a := recordapply.New("", func() fs.FS { return mem })
	a.Stage = func(path string, content []byte, create bool) (func() error, func(), error) {
		return func() error {
			if _, exists := mem[path]; create && exists {
				return recordapply.Appeared(path)
			}
			writes[path] = content
			mem[path] = &fstest.MapFile{Data: content}
			return nil
		}, func() {}, nil
	}
	a.Remove = func(path string) error {
		writes[path] = nil
		delete(mem, path)
		return nil
	}
	return a
}
