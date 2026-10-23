package golang

import (
	"fmt"
	"slices"
	"strings"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// sortedCompact owns its result; comparator equality defines row identity.
func sortedCompact[T any](rows []T, compare func(T, T) int) []T {
	out := slices.Clone(rows)
	slices.SortFunc(out, compare)
	return slices.CompactFunc(out, func(a, b T) bool { return compare(a, b) == 0 })
}

// containsCanonical tests exact set containment in linear time. Both inputs
// are canonical under compare; path or pattern coverage is not row identity.
func containsCanonical[T any](current, recorded []T, compare func(T, T) int) bool {
	i := 0
	for _, want := range recorded {
		for i < len(current) && compare(current[i], want) < 0 {
			i++
		}
		if i == len(current) || compare(current[i], want) != 0 {
			return false
		}
	}
	return true
}

func compareNativeNamespaces(a, b runtimeinput.ScratchNamespace) int {
	return witnesscache.CompareScratchNamespaces(
		witnesscache.ScratchNamespace{Dir: a.Dir, Pattern: a.Pattern},
		witnesscache.ScratchNamespace{Dir: b.Dir, Pattern: b.Pattern})
}

func canonicalReviewedExclusions(rows []string) []string { return sortedCompact(rows, strings.Compare) }

func canonicalReviewedNamespaces(rows []runtimeinput.ScratchNamespace) []runtimeinput.ScratchNamespace {
	return sortedCompact(rows, compareNativeNamespaces)
}

func reviewedNamespaceRows(rows []runtimeinput.ScratchNamespace) []string {
	out := make([]string, 0, len(rows))
	for _, ns := range canonicalReviewedNamespaces(rows) {
		out = append(out, string(quotedList([]string{ns.Dir, ns.Pattern})))
	}
	return out
}

func toRecordedNamespaces(rows []runtimeinput.ScratchNamespace) []witnesscache.ScratchNamespace {
	out := make([]witnesscache.ScratchNamespace, 0, len(rows))
	for _, ns := range rows {
		out = append(out, witnesscache.ScratchNamespace{Dir: ns.Dir, Pattern: ns.Pattern})
	}
	return sortedCompact(out, witnesscache.CompareScratchNamespaces)
}

// recordedDeclaration couples provenance and withdrawal through one field.
// Vouches are absent: only native used acceptances license them.
type recordedDeclaration interface {
	recordInto(*witnesscache.Record)
	refusal(witnesscache.Record) string
}

type recordedSet[T any] struct {
	current         []T
	compare         func(T, T) int
	fieldAccessor   func(*witnesscache.Record) *[]T
	withdrawnReason string
}

func newRecordedSet[T any](current []T, compare func(T, T) int, field func(*witnesscache.Record) *[]T, reason string) recordedSet[T] {
	return recordedSet[T]{sortedCompact(current, compare), compare, field, reason}
}

func (s recordedSet[T]) recordInto(rec *witnesscache.Record) {
	*s.fieldAccessor(rec) = slices.Clone(s.current)
}

func (s recordedSet[T]) refusal(rec witnesscache.Record) string {
	if !containsCanonical(s.current, *s.fieldAccessor(&rec), s.compare) {
		return s.withdrawnReason
	}
	return ""
}

type recordedDeclarations []recordedDeclaration

func (d recordedDeclarations) recordInto(rec *witnesscache.Record) {
	for _, declaration := range d {
		declaration.recordInto(rec)
	}
}

func (d recordedDeclarations) refusal(rec witnesscache.Record) string {
	for _, declaration := range d {
		if why := declaration.refusal(rec); why != "" {
			return why
		}
	}
	return ""
}

func declarationsFor(g *captureGroup) recordedDeclarations {
	return recordedDeclarations{
		newRecordedSet(g.excludedPaths, strings.Compare, func(r *witnesscache.Record) *[]string { return &r.ObservationExclusions }, refusedWithdrawnExclusion),
		newRecordedSet(toRecordedNamespaces(g.scratchNamespaces), witnesscache.CompareScratchNamespaces, func(r *witnesscache.Record) *[]witnesscache.ScratchNamespace { return &r.ObservationNamespaces }, refusedWithdrawnNamespace),
	}
}

// validateReviewedRows checks record facts; resolved-tree exclusion placement
// remains the normalization step's responsibility.
func validateReviewedRows(cfg *stipulatorv1.GoInvocationConfig) error {
	for _, p := range cfg.GetExcludedPaths() {
		if err := validateExcludedPathForm(p); err != nil {
			return err
		}
	}
	for _, v := range cfg.GetDynamicStateVouches() {
		if _, err := reviewedVouchIdentity(v); err != nil {
			return err
		}
	}
	return validateScratchNamespaces(cfg)
}

// normalizeReviewedRows consumes a record accepted by validateReviewedRows.
func normalizeReviewedRows(cfg *stipulatorv1.GoInvocationConfig, n *NormalizedInvocation) {
	n.ExcludedPaths = canonicalReviewedExclusions(cfg.GetExcludedPaths())
	var vouches []string
	for _, v := range cfg.GetDynamicStateVouches() {
		identity, _ := reviewedVouchIdentity(v)
		vouches = append(vouches, identity)
	}
	n.Vouches = sortedCompact(vouches, strings.Compare)
	var namespaces []runtimeinput.ScratchNamespace
	for _, ns := range cfg.GetScratchNamespaces() {
		namespaces = append(namespaces, runtimeinput.ScratchNamespace{Dir: ns.GetDir(), Pattern: ns.GetPattern()})
	}
	n.ScratchNamespaces = canonicalReviewedNamespaces(namespaces)
}

// reviewedKeySegments partitions capture without changing the persistent
// record address. Labels, ordering and nested pair quoting are wire-stable.
func reviewedKeySegments(n *NormalizedInvocation) []keySegment {
	return []keySegment{
		{"exclusions", quotedList(canonicalReviewedExclusions(n.ExcludedPaths))},
		{"vouches", quotedList(sortedCompact(n.Vouches, strings.Compare))},
		{"namespaces", quotedList(reviewedNamespaceRows(n.ScratchNamespaces))},
	}
}

// reviewedVouchIdentity delegates reviewed spelling to the native grammar.
func reviewedVouchIdentity(v *stipulatorv1.DynamicStateVouch) (string, error) {
	pkg, name := v.GetPackage(), v.GetVariable()
	if pkg == "" {
		return "", fmt.Errorf("dynamic_state_vouches entry needs a package import path")
	}
	if name == "" {
		return "", fmt.Errorf("dynamic_state_vouches entry for %q needs a variable name", pkg)
	}
	if strings.Contains(pkg, ":") {
		return "", fmt.Errorf("dynamic_state_vouches package %q carries a colon", pkg)
	}
	identity, err := gofresh.ParseVouchEntry(pkg + ":" + name)
	if err != nil {
		return "", fmt.Errorf("dynamic_state_vouches entry for %q: %w", pkg, err)
	}
	return identity, nil
}
