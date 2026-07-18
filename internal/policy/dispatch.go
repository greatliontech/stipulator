package policy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"google.golang.org/protobuf/proto"
)

// Backend is one language backend's policy surface: the dispatch target
// for the payload case it claims. Implementations live outside this
// package — the core hands each invocation's typed payload over opaquely
// and never interprets its contents (REQ-policy-backend-neutral).
type Backend interface {
	// ValidateInvocation checks one dispatched payload against the
	// backend's own configuration semantics — for Go, module-root
	// hermeticity today and the full normalization surface later. The
	// payload is the invocation's typed configuration message; a backend
	// receiving a type it does not own must refuse it.
	ValidateInvocation(invocation string, payload proto.Message) error
}

// Invocation is the orchestration-facing projection of one policy
// invocation: canonical identity, the claiming backend's name, and the
// reviewed timeout. Orchestration observes these facts — and the health
// facts execution later derives — never the typed payload.
type Invocation struct {
	Name    string
	Backend string
	Timeout time.Duration
}

// Dispatch validates the policy's canonical envelope and dispatches every
// invocation's typed payload to the backend named by its payload case,
// returning the orchestration-facing invocation facts in record order. A
// payload case no registered backend claims, or a payload the claiming
// backend refuses, rejects the policy whole: an unsupported invocation can
// never silently drop out of the reviewed suite.
func Dispatch(p *stipulatorv1.TestPolicy, backends map[string]Backend) ([]Invocation, error) {
	if err := Validate(p); err != nil {
		return nil, err
	}
	invs := make([]Invocation, 0, len(p.GetInvocations()))
	for _, inv := range p.GetInvocations() {
		r := inv.ProtoReflect()
		// Validate guaranteed a set payload case, and the case name IS
		// the backend name — one source, so the two cannot disagree.
		fd := r.WhichOneof(r.Descriptor().Oneofs().ByName("config"))
		backend := string(fd.Name())
		b, ok := backends[backend]
		if !ok {
			return nil, fmt.Errorf("invocation %q: no registered backend claims payload case %q", inv.GetName(), backend)
		}
		if err := b.ValidateInvocation(inv.GetName(), r.Get(fd).Message().Interface()); err != nil {
			return nil, fmt.Errorf("invocation %q: %w", inv.GetName(), err)
		}
		invs = append(invs, Invocation{
			Name:    inv.GetName(),
			Backend: backend,
			Timeout: inv.GetTimeout().AsDuration(),
		})
	}
	return invs, nil
}

// ErrRecord classifies a policy loading failure as a record problem — the
// committed record is missing or invalid — as opposed to an operational
// fault reading it. A record problem is a fact about the tree; an
// operational fault says nothing about it.
var ErrRecord = errors.New("policy record problem")

// recordError marks a loading failure as ErrRecord without reshaping its
// message.
type recordError struct{ err error }

func (e recordError) Error() string { return e.err.Error() }
func (e recordError) Unwrap() error { return e.err }
func (recordError) Is(target error) bool {
	return target == ErrRecord
}

// Load reads the committed policy record from its fixed location under
// root, strict-parses it, and dispatches it through backends: the one
// loading seam every consumer shares, so what executes is always the
// validated form of what was reviewed. An absent record is an error — the
// policy is explicit, never assumed (REQ-policy-explicit). Missing and
// invalid records match ErrRecord; a read fault that says nothing about
// the record's content does not.
func Load(root string, backends map[string]Backend) (*stipulatorv1.TestPolicy, []Invocation, error) {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(Path)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, recordError{fmt.Errorf("no accepted test policy at %s; run `stipulator policy init` to derive one", Path)}
	}
	if err != nil {
		return nil, nil, err
	}
	p, err := Parse(raw)
	if err != nil {
		return nil, nil, recordError{err}
	}
	invs, err := Dispatch(p, backends)
	if err != nil {
		return nil, nil, recordError{err}
	}
	return p, invs, nil
}
