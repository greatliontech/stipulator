package golang

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"time"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/internal/resolutioncache"
	"github.com/greatliontech/stipulator/internal/trace"
)

// beforeServingCloseForTest places a source or configuration edit between
// opening multi-view serving checks and their closing interval re-observation.
var beforeServingCloseForTest func()

// The executable content identifies the linked classifier, importer and
// frontend together, including local replacements without module checksums.
// Sample at process initialization, before a later installation can replace
// the executable's pathname. Linux exposes the running image independently
// of that pathname. Other hosts use the executable supplied at startup.
var sourceClassifierIdentity, sourceClassifierError = classifierIdentity()

func classifierIdentity() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "linux" {
		path = "/proc/self/exe"
	}
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func checkClassifierIdentity() error {
	if sourceClassifierError != nil || sourceClassifierIdentity == "" {
		return fmt.Errorf("resolution classifier identity unavailable: %v", sourceClassifierError)
	}
	return nil
}

// selectionEngine constructs the gofresh.Session for one resolution build
// selection: it enforces the same per-member toolchain-provenance prerequisite
// as the typed view and binds the analyzer executable's content digest into
// BuildDescription.Inputs so Guards.BuildConfig invalidates records across
// classifier binary rebuilds (REQ-evidence-resolution-freshness).
func selectionEngine(ctx context.Context, view *selectedContext, sample toolchainSample) (*gofresh.Session, error) {
	if err := checkClassifierIdentity(); err != nil {
		return nil, err
	}
	if err := checkSelectionMembers(ctx, view.root, view.env, view.members, view.selection.toolchain, sample); err != nil {
		return nil, err
	}
	return newEngine(ctx, view.directory, view.env, view.flags, view.root, "stipulator/source-classifier="+sourceClassifierIdentity)
}

type capturedSnapshot struct {
	snap     *gofresh.Snapshot
	subjects []gofresh.Subject
}

// sourceViewCapture holds one build selection's SourceResult observation across
// a set of symbols, including per-package load failures when isolated.
type sourceViewCapture struct {
	present    map[gofresh.Subject]gofresh.Fingerprint
	absent     map[gofresh.Subject]bool
	failedPkgs map[string]error
	snaps      []capturedSnapshot
	probed     map[gofresh.Subject]bool
}

// captureForTest, when set, rewrites a capture under its phase
// ("opening" or "publish") before any reader sees it — a seam so a test
// plants a subject either capture omitted and reads it named in the
// account.
var captureForTest func(phase string, capture map[gofresh.Subject]gofresh.Fingerprint) map[gofresh.Subject]gofresh.Fingerprint

func narrowSourceSnapshot(ctx context.Context, engine *gofresh.Session, subjects []gofresh.Subject) (*capturedSnapshot, map[gofresh.Subject]gofresh.Fingerprint, map[gofresh.Subject]bool, error) {
	unknownSubjects := map[gofresh.Subject]bool{}
	remaining := slices.Clone(subjects)
	var snap *gofresh.Snapshot
	for len(remaining) > 0 {
		var err error
		snap, err = engine.Snapshot(ctx, remaining, gofresh.SourceResult)
		var unknown *gofresh.UnknownSubjectsError
		if errors.As(err, &unknown) {
			gone := make(map[gofresh.Subject]bool, len(unknown.Subjects))
			for _, subject := range unknown.Subjects {
				gone[subject] = true
				unknownSubjects[subject] = true
			}
			if len(gone) == 0 {
				return nil, nil, nil, err
			}
			kept := remaining[:0]
			for _, subject := range remaining {
				if !gone[subject] {
					kept = append(kept, subject)
				}
			}
			remaining = kept
			snap = nil
			continue
		}
		if err != nil {
			return nil, nil, nil, err
		}
		break
	}
	fps := map[gofresh.Subject]gofresh.Fingerprint{}
	var captured *capturedSnapshot
	if len(remaining) > 0 {
		var err error
		fps, err = snap.CaptureBatch(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
		captured = &capturedSnapshot{snap: snap, subjects: slices.Clone(remaining)}
	}
	return captured, fps, unknownSubjects, nil
}

// captureSourceView builds a SourceResult view over symbols under selection key,
// narrowing undeclared subjects and isolating package-specific derivation
// failures when multiple packages are queried (REQ-evidence-resolution-freshness).
func (s *Served) captureSourceView(endpoint *selectionEndpoint, key string, symbols []string, phase string) (sourceViewCapture, bool) {
	if !s.inventoryComplete() {
		return sourceViewCapture{}, false
	}
	selected, err := endpoint.selected(key)
	if err != nil {
		s.degraded = append(s.degraded, fmt.Sprintf("%s %q: %v", phase, key, err))
		return sourceViewCapture{}, false
	}
	cap := sourceViewCapture{
		present:    map[gofresh.Subject]gofresh.Fingerprint{},
		absent:     map[gofresh.Subject]bool{},
		failedPkgs: map[string]error{},
		probed:     map[gofresh.Subject]bool{},
	}
	var subjects []gofresh.Subject
	seenSubject := map[gofresh.Subject]bool{}
	subjectsByPkg := map[string][]gofresh.Subject{}
	symbolsByPkg := map[string][]string{}
	seenSymbol := map[string]bool{}
	for _, sym := range symbols {
		subject, ok := s.subjectOf(sym)
		if !ok {
			continue
		}
		cap.probed[subject] = true
		if s.set != nil && !s.inventory[key][subject.Package] {
			cap.absent[subject] = true
			continue
		}
		if !seenSymbol[sym] {
			seenSymbol[sym] = true
			symbolsByPkg[subject.Package] = append(symbolsByPkg[subject.Package], sym)
		}
		if !seenSubject[subject] {
			seenSubject[subject] = true
			subjects = append(subjects, subject)
			subjectsByPkg[subject.Package] = append(subjectsByPkg[subject.Package], subject)
		}
	}
	for pkg := range symbolsByPkg {
		sort.Strings(symbolsByPkg[pkg])
	}
	if len(subjects) == 0 && captureForTest == nil {
		if err := checkClassifierIdentity(); err != nil {
			s.degraded = append(s.degraded, fmt.Sprintf("%s %q: %v", phase, key, err))
			return sourceViewCapture{}, false
		}
		if err := checkSelectionMembers(s.ctx, selected.root, selected.env, selected.members, selected.selection.toolchain, s.sample); err != nil {
			s.degraded = append(s.degraded, fmt.Sprintf("%s %q: %v", phase, key, err))
			return sourceViewCapture{}, false
		}
		return cap, true
	}
	engine, err := endpoint.session(selected, s.sample)
	if err != nil {
		s.degraded = append(s.degraded, fmt.Sprintf("%s %q: %v", phase, key, err))
		return sourceViewCapture{}, false
	}
	snap, fps, unk, batchErr := narrowSourceSnapshot(s.ctx, engine, subjects)
	if batchErr == nil {
		for u := range unk {
			cap.absent[u] = true
		}
		if captureForTest != nil && (phase == "opening" || phase == "publish") {
			fps = captureForTest(phase, fps)
		}
		cap.present = fps
		if snap != nil {
			cap.snaps = []capturedSnapshot{*snap}
		}
		return cap, true
	}

	pkgs := make([]string, 0, len(subjectsByPkg))
	for pkg := range subjectsByPkg {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)
	if len(pkgs) == 1 {
		if phase == "selection" && len(cap.absent) == 0 {
			s.degraded = append(s.degraded, fmt.Sprintf("selection %q: %v", key, batchErr))
			return sourceViewCapture{}, false
		}
		pkg := pkgs[0]
		cap.failedPkgs[pkg] = batchErr
		for _, sym := range symbolsByPkg[pkg] {
			s.degraded = append(s.degraded, fmt.Sprintf("%s %s under %q: %v", phase, sym, key, batchErr))
		}
		return cap, true
	}

	succeeded := 0
	for _, pkg := range pkgs {
		pkgSnap, pkgFPs, pkgUnk, pkgErr := narrowSourceSnapshot(s.ctx, engine, subjectsByPkg[pkg])
		if pkgErr != nil {
			cap.failedPkgs[pkg] = pkgErr
			continue
		}
		succeeded++
		for subj, fp := range pkgFPs {
			cap.present[subj] = fp
		}
		for u := range pkgUnk {
			cap.absent[u] = true
		}
		if pkgSnap != nil {
			cap.snaps = append(cap.snaps, *pkgSnap)
		}
	}
	if succeeded == 0 && len(cap.absent) == 0 && phase == "selection" {
		s.degraded = append(s.degraded, fmt.Sprintf("selection %q: %v", key, batchErr))
		return sourceViewCapture{}, false
	}
	for _, pkg := range pkgs {
		if pkgErr := cap.failedPkgs[pkg]; pkgErr != nil {
			for _, sym := range symbolsByPkg[pkg] {
				s.degraded = append(s.degraded, fmt.Sprintf("%s %s under %q: %v", phase, sym, key, pkgErr))
			}
		}
	}
	if captureForTest != nil && (phase == "opening" || phase == "publish") {
		cap.present = captureForTest(phase, cap.present)
	}
	return cap, true
}

func recordMatchesSelections(rec resolutioncache.Record, sels []buildSelection) bool {
	if len(rec.Views) != len(sels) {
		return false
	}
	for i, sel := range sels {
		if rec.Views[i].Selection != sel.key() {
			return false
		}
	}
	return true
}

func precheckCandidateRecord(rec resolutioncache.Record, pkg string, hasPkg bool, sels []buildSelection, inventory map[string]map[string]bool) (resolutionReason, bool) {
	if !hasPkg || pkg != rec.Package {
		if !inventory[rec.Selection][rec.Package] {
			return resolutionNoLongerDeclared.with(""), false
		}
		return resolutionSourceDependencies.with(""), false
	}
	if !inventory[rec.Selection][pkg] {
		return resolutionNoLongerDeclared.with(""), false
	}
	if !recordMatchesSelections(rec, sels) {
		return resolutionSourceDependencies.with(""), false
	}
	for _, v := range rec.Views {
		if v.Present && !inventory[v.Selection][pkg] {
			return resolutionSourceDependencies.with(""), false
		}
	}
	return resolutionReason{}, true
}

func checkCandidateInView(rec resolutioncache.Record, viewIdx int, key string, cap sourceViewCapture) (resolutionReason, bool, bool) {
	subject, ok := recordSubject(rec)
	if !ok || cap.failedPkgs[rec.Package] != nil {
		return resolutionReason{}, false, false
	}
	v := rec.Views[viewIdx]
	if v.Present {
		if cap.absent[subject] {
			if key == rec.Selection {
				return resolutionNoLongerDeclared.with(""), true, false
			}
			return resolutionSourceDependencies.with(""), true, false
		}
		fp, captured := cap.present[subject]
		if !captured {
			return resolutionNoCurrentCapture.with(""), true, false
		}
		if why := closureMovedReason(v.Fingerprint, fp); why != (resolutionReason{}) {
			return why, true, false
		}
		return resolutionReason{}, false, true
	}
	if _, nowPresent := cap.present[subject]; nowPresent || !cap.absent[subject] {
		return resolutionSourceDependencies.with(""), true, false
	}
	return resolutionReason{}, false, true
}

func isSourceDriftErr(err error) bool {
	if errors.Is(err, gofresh.ErrViewChanged) {
		return true
	}
	var unk *gofresh.UnknownSubjectsError
	return errors.As(err, &unk) && unk.Scope == gofresh.SelectedSource
}

// serveRecords evaluates stored multi-view SourceResult records across all
// policy build selections and closes their observation intervals before
// granting any served answer.
func (s *Served) serveRecords(endpoint *selectionEndpoint) {
	if !s.inventoryComplete() || s.set == nil {
		if beforeServingCloseForTest != nil {
			beforeServingCloseForTest()
		}
		return
	}
	seenIdentity := map[string]bool{}
	seenRecorded := map[string]bool{}
	newestKeyBySym := map[string]string{}
	reasonsByKey := map[string]resolutionReason{}
	var recordedSymbols []string
	var candidates []resolutioncache.Record

	for _, rec := range resolutioncache.Load(s.dir) {
		if !s.set[rec.Symbol] {
			continue
		}
		if _, known := s.selection(rec.Selection); !known {
			continue
		}
		id := rec.Key()
		if seenIdentity[id] {
			continue
		}
		seenIdentity[id] = true
		if _, hasNewest := newestKeyBySym[rec.Symbol]; !hasNewest {
			newestKeyBySym[rec.Symbol] = id
		}
		pkg, ok := s.packageOf(rec.Symbol)
		if ok && !seenRecorded[rec.Symbol] {
			seenRecorded[rec.Symbol] = true
			recordedSymbols = append(recordedSymbols, rec.Symbol)
		}
		if why, keep := precheckCandidateRecord(rec, pkg, ok, s.sels, s.inventory); !keep {
			reasonsByKey[id] = why
			continue
		}
		candidates = append(candidates, rec)
	}
	sort.Strings(recordedSymbols)

	failedPkgs := map[string]bool{}
	capsByKey := make(map[string]sourceViewCapture, len(s.sels))
	for viewIdx, sel := range s.sels {
		if len(candidates) == 0 {
			break
		}
		key := sel.key()
		selStart := time.Now()
		var probeSymbols []string
		for _, sym := range recordedSymbols {
			pkg, ok := s.packageOf(sym)
			if ok && !failedPkgs[pkg] {
				probeSymbols = append(probeSymbols, sym)
			}
		}
		cap, ok := s.captureSourceView(endpoint, key, probeSymbols, "selection")
		if !ok {
			trace.Emit(s.ctx, trace.DomainResolve, "serve_selection", key,
				trace.Int("records", len(candidates)),
				trace.Int("served", 0),
				trace.Int64("duration_ms", time.Since(selStart).Milliseconds()),
			)
			candidates = nil
			break
		}
		s.checkedViews[key] = cap
		capsByKey[key] = cap
		for pkg := range cap.failedPkgs {
			failedPkgs[pkg] = true
		}

		kept := candidates[:0]
		for _, rec := range candidates {
			why, hasWhy, keep := checkCandidateInView(rec, viewIdx, key, cap)
			if !keep {
				if hasWhy {
					reasonsByKey[rec.Key()] = why
				}
				continue
			}
			kept = append(kept, rec)
		}
		candidates = kept
		trace.Emit(s.ctx, trace.DomainResolve, "serve_selection", key,
			trace.Int("records", len(probeSymbols)),
			trace.Int("surviving", len(candidates)),
			trace.Int64("duration_ms", time.Since(selStart).Milliseconds()),
		)
	}

	for _, rec := range candidates {
		if _, exists := s.served[rec.Symbol]; !exists {
			s.served[rec.Symbol] = rec
		}
	}
	for sym, key := range newestKeyBySym {
		if _, served := s.served[sym]; !served {
			if why, hasWhy := reasonsByKey[key]; hasWhy {
				s.reasons[sym] = why
			}
		}
	}
	s.closeServedViews(capsByKey)
}

type openViewUnit struct {
	key     string
	unit    *gofresh.CheckingUnit
	symbols []string
}

func (s *Served) closeServedViews(capsByKey map[string]sourceViewCapture) {
	if len(s.served) == 0 {
		if beforeServingCloseForTest != nil {
			beforeServingCloseForTest()
			if currentSels, _, err := policyBuildSelections(s.dir); err == nil {
				s.sels = currentSels
			}
			clear(s.checkedViews)
			s.initialEndpoint = nil
		}
		return
	}
	var openUnits []openViewUnit
	defer func() {
		for _, ou := range openUnits {
			ou.unit.Abort()
		}
	}()

	revoke := func(sym string, reason resolutionReason) bool {
		if _, ok := s.served[sym]; !ok {
			return false
		}
		delete(s.served, sym)
		s.reasons[sym] = reason
		return true
	}
	revoked := false

	for viewIdx, sel := range s.sels {
		key := sel.key()
		symbolsBySubject := map[gofresh.Subject][]string{}
		for sym, rec := range s.served {
			if !rec.Views[viewIdx].Present {
				continue
			}
			if subj, ok := recordSubject(rec); ok {
				symbolsBySubject[subj] = append(symbolsBySubject[subj], sym)
			}
		}
		if len(symbolsBySubject) == 0 {
			continue
		}
		for _, cs := range capsByKey[key].snaps {
			var subjs []gofresh.Subject
			var syms []string
			for _, subj := range cs.subjects {
				if list := symbolsBySubject[subj]; len(list) > 0 {
					subjs = append(subjs, subj)
					syms = append(syms, list...)
				}
			}
			if len(subjs) == 0 {
				continue
			}
			sort.Strings(syms)
			binding, err := cs.snap.BindRuntime(s.ctx, gofresh.RuntimeDescription{})
			if err == nil {
				var unit *gofresh.CheckingUnit
				unit, err = binding.BeginCheck(subjs)
				if err == nil {
					openUnits = append(openUnits, openViewUnit{key: key, unit: unit, symbols: syms})
					continue
				}
			}
			reason := resolutionNoClosingCapture.with("")
			if isSourceDriftErr(err) {
				reason = resolutionSourceMovedDuringServing.with("")
			} else {
				s.degraded = append(s.degraded, fmt.Sprintf("closing %q: %v", key, err))
			}
			for _, sym := range syms {
				if revoke(sym, reason) {
					revoked = true
				}
			}
		}
	}

	if beforeServingCloseForTest != nil {
		beforeServingCloseForTest()
	}

	if len(s.served) > 0 {
		if err := checkClassifierIdentity(); err != nil {
			s.degraded = append(s.degraded, "closing: "+err.Error())
			for sym := range s.served {
				if revoke(sym, resolutionNoClosingCapture.with("")) {
					revoked = true
				}
			}
		} else if currentSels, _, err := policyBuildSelections(s.dir); err != nil {
			s.degraded = append(s.degraded, "closing: "+err.Error())
			for sym := range s.served {
				if revoke(sym, resolutionNoClosingCapture.with("")) {
					revoked = true
				}
			}
		} else if !sameSelectionKeys(currentSels, s.sels) {
			s.sels = currentSels
			for sym := range s.served {
				if revoke(sym, resolutionSourceMovedDuringServing.with("")) {
					revoked = true
				}
			}
		}
	}

	for _, ou := range openUnits {
		if len(s.served) == 0 {
			break
		}
		if _, err := ou.unit.Close(s.ctx); err != nil {
			reason := resolutionNoClosingCapture.with("")
			if isSourceDriftErr(err) {
				reason = resolutionSourceMovedDuringServing.with("")
			} else {
				s.degraded = append(s.degraded, fmt.Sprintf("closing %q: %v", ou.key, err))
			}
			for _, sym := range ou.symbols {
				if revoke(sym, reason) {
					revoked = true
				}
			}
		}
	}

	if len(s.served) > 0 {
		var closingEndpoint *selectionEndpoint
		getClosingEndpoint := func() *selectionEndpoint {
			if closingEndpoint == nil {
				closingEndpoint = newSelectionEndpoint(s.ctx, s.dir, s.sels)
			}
			return closingEndpoint
		}
		for viewIdx, sel := range s.sels {
			key := sel.key()
			var checkInventorySyms []string
			var absentInDeclaredPkg []string
			for sym, rec := range s.served {
				needInv := !rec.Views[viewIdx].Present || len(symbolPackageCandidates(sym)) > 1
				if needInv {
					checkInventorySyms = append(checkInventorySyms, sym)
				}
				if !rec.Views[viewIdx].Present && s.inventory[key][rec.Package] {
					absentInDeclaredPkg = append(absentInDeclaredPkg, sym)
				}
			}
			if len(checkInventorySyms) == 0 {
				continue
			}
			sort.Strings(checkInventorySyms)
			if err := s.verifyInventoryUnchanged(getClosingEndpoint(), key, checkInventorySyms); err != nil {
				reason := resolutionNoClosingCapture.with("")
				if errors.Is(err, errInventoryChanged) {
					reason = resolutionSourceMovedDuringServing.with("")
				} else {
					s.degraded = append(s.degraded, fmt.Sprintf("closing %q: package inventory: %v", key, err))
				}
				for _, sym := range checkInventorySyms {
					if revoke(sym, reason) {
						revoked = true
					}
				}
				continue
			}
			if len(absentInDeclaredPkg) > 0 {
				sort.Strings(absentInDeclaredPkg)
				closeCap, ok := s.captureSourceView(getClosingEndpoint(), key, absentInDeclaredPkg, "closing")
				if !ok {
					for _, sym := range absentInDeclaredPkg {
						if revoke(sym, resolutionNoClosingCapture.with("")) {
							revoked = true
						}
					}
					continue
				}
				for _, sym := range absentInDeclaredPkg {
					rec, stillServed := s.served[sym]
					if !stillServed {
						continue
					}
					subj, ok := recordSubject(rec)
					if !ok || closeCap.failedPkgs[rec.Package] != nil {
						if revoke(sym, resolutionNoClosingCapture.with("")) {
							revoked = true
						}
						continue
					}
					if _, nowPresent := closeCap.present[subj]; nowPresent || !closeCap.absent[subj] {
						if revoke(sym, resolutionSourceMovedDuringServing.with("")) {
							revoked = true
						}
					}
				}
			}
		}
	}

	if revoked || beforeServingCloseForTest != nil {
		clear(s.checkedViews)
		s.initialEndpoint = nil
	}
}

func sameSelectionKeys(a, b []buildSelection) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].key() != b[i].key() {
			return false
		}
	}
	return true
}

// openingCapture captures multi-view SourceResult entries for every unserved
// symbol before the typed load opens, reusing checkedViews from NewServed when
// no mid-serving drift, test hook, or reopen intervened.
func (s *Served) openingCapture() {
	s.openingViews = map[string]sourceViewCapture{}
	if len(s.unserved) == 0 {
		return
	}
	canReuseInitial := !s.reopened && beforeServingCloseForTest == nil && captureForTest == nil && len(s.degraded) == 0 && s.inventoryComplete()
	endpoint := s.initialEndpoint
	if s.reopened || endpoint == nil {
		endpoint = newSelectionEndpoint(s.ctx, s.dir, s.sels)
		if s.set != nil {
			s.inventoryPackages(endpoint, s.unserved)
		}
	}
	for _, sel := range s.sels {
		key := sel.key()
		if canReuseInitial {
			if reused, ok := s.reuseCheckedView(key, s.unserved); ok {
				s.openingViews[key] = reused
				continue
			}
		}
		if cap, ok := s.captureSourceView(endpoint, key, s.unserved, "opening"); ok {
			s.openingViews[key] = cap
		}
	}
}

func (s *Served) reuseCheckedView(key string, symbols []string) (sourceViewCapture, bool) {
	checked, hasKey := s.checkedViews[key]
	if !hasKey {
		return sourceViewCapture{}, false
	}
	out := sourceViewCapture{
		present:    map[gofresh.Subject]gofresh.Fingerprint{},
		absent:     map[gofresh.Subject]bool{},
		failedPkgs: map[string]error{},
		probed:     map[gofresh.Subject]bool{},
	}
	for _, sym := range symbols {
		subject, ok := s.subjectOf(sym)
		if !ok {
			continue
		}
		if s.set != nil && !s.inventory[key][subject.Package] {
			out.probed[subject] = true
			out.absent[subject] = true
			continue
		}
		if !checked.probed[subject] || checked.failedPkgs[subject.Package] != nil {
			return sourceViewCapture{}, false
		}
		out.probed[subject] = true
		if fp, ok := checked.present[subject]; ok {
			out.present[subject] = fp
		} else if checked.absent[subject] {
			out.absent[subject] = true
		} else {
			return sourceViewCapture{}, false
		}
	}
	return out, true
}

// publishPending captures closing multi-view SourceResult entries once across
// all policy selections and installs records for every pending resolved symbol
// whose opening and closing views agree under unchanged package boundaries.
func (s *Served) publishPending() {
	if s.ctx.Err() != nil {
		return
	}
	pubStart := time.Now()
	resKeys := make([]string, 0, len(s.pending))
	seenSym := map[string]bool{}
	var allSymbols []string
	for key, syms := range s.pending {
		resKeys = append(resKeys, key)
		for _, sym := range syms {
			if !seenSym[sym] {
				seenSym[sym] = true
				allSymbols = append(allSymbols, sym)
			}
		}
	}
	sort.Strings(resKeys)
	sort.Strings(allSymbols)

	policyMoved := false
	policyUnavailable := false
	if currentSels, _, err := policyBuildSelections(s.dir); err != nil {
		s.degraded = append(s.degraded, "publish: "+err.Error())
		policyUnavailable = true
	} else if !sameSelectionKeys(currentSels, s.sels) {
		policyMoved = true
	}

	endpoint := newSelectionEndpoint(s.ctx, s.dir, s.sels)
	closingViews := make(map[string]sourceViewCapture, len(s.sels))
	closingOK := make(map[string]bool, len(s.sels))
	inventoryMoved := make(map[string]bool, len(s.sels))
	if !policyUnavailable && !policyMoved && len(s.openingViews) > 0 {
		for _, sel := range s.sels {
			key := sel.key()
			if err := s.verifyInventoryUnchanged(endpoint, key, allSymbols); err != nil {
				if errors.Is(err, errInventoryChanged) {
					inventoryMoved[key] = true
				} else {
					s.degraded = append(s.degraded, fmt.Sprintf("publish %q: package inventory: %v", key, err))
				}
				continue
			}
			cap, ok := s.captureSourceView(endpoint, key, allSymbols, "publish")
			if ok {
				closingViews[key] = cap
				closingOK[key] = true
			}
		}
	}

	type skipStatus uint8
	const (
		skipNone skipStatus = iota
		skipUncaptured
		skipUnopened
		skipMoved
	)

	for _, resKey := range resKeys {
		symbols := s.pending[resKey]
		var recs []resolutioncache.Record
		var account publishAccount
		for _, symbol := range symbols {
			a := s.answers[symbol]
			if policyUnavailable {
				account.uncaptured = append(account.uncaptured, symbol)
				continue
			}
			if policyMoved {
				account.moved = append(account.moved, symbol)
				continue
			}
			subject, ok := s.subjectOf(symbol)
			if !ok {
				if len(s.openingViews) == 0 {
					account.unopened = append(account.unopened, symbol)
				} else {
					account.uncaptured = append(account.uncaptured, symbol)
				}
				continue
			}
			if a.pkg != subject.Package {
				account.moved = append(account.moved, symbol)
				continue
			}
			views := make([]resolutioncache.ViewEntry, 0, len(s.sels))
			status := skipNone
			for _, sel := range s.sels {
				vKey := sel.key()
				if inventoryMoved[vKey] {
					status = skipMoved
					break
				}
				openCap, hasOpen := s.openingViews[vKey]
				if !hasOpen || openCap.failedPkgs[subject.Package] != nil {
					status = skipUnopened
					break
				}
				openFP, openPresent := openCap.present[subject]
				openAbsent := openCap.absent[subject]
				if !openPresent && !openAbsent {
					status = skipUnopened
					break
				}
				closeCap, hasClose := closingViews[vKey], closingOK[vKey]
				if !hasClose || closeCap.failedPkgs[subject.Package] != nil {
					status = skipUncaptured
					break
				}
				closeFP, closePresent := closeCap.present[subject]
				closeAbsent := closeCap.absent[subject]
				if !closePresent && !closeAbsent {
					status = skipUncaptured
					break
				}
				if openPresent != closePresent {
					status = skipMoved
					break
				}
				if openPresent {
					if closureMovedReason(openFP, closeFP) != (resolutionReason{}) {
						status = skipMoved
						break
					}
					views = append(views, resolutioncache.ViewEntry{
						Selection:   vKey,
						Present:     true,
						Fingerprint: openFP,
					})
				} else {
					views = append(views, resolutioncache.ViewEntry{
						Selection: vKey,
						Present:   false,
					})
				}
			}
			switch status {
			case skipUncaptured:
				account.uncaptured = append(account.uncaptured, symbol)
				continue
			case skipUnopened:
				account.unopened = append(account.unopened, symbol)
				continue
			case skipMoved:
				account.moved = append(account.moved, symbol)
				continue
			}
			rec := resolutioncache.Record{
				Selection:          resKey,
				Symbol:             symbol,
				Views:              views,
				Resolution:         resolutionWire(a.res),
				Shape:              a.shape,
				Package:            a.pkg,
				WitnessClass:       classWire(a.class),
				WitnessClassReason: a.reason,
				NeverServe:         s.refusals[symbol],
			}
			if !resolutioncache.AdmitsRecord(rec) {
				account.refused = append(account.refused, symbol)
				continue
			}
			recs = append(recs, rec)
		}
		if len(recs) > 0 {
			if err := resolutioncache.InstallAll(s.dir, recs); err != nil {
				s.degraded = append(s.degraded, fmt.Sprintf("publish %q: %v", resKey, err))
			} else {
				account.installed = len(recs)
			}
		}
		if s.published == nil {
			s.published = map[string]publishAccount{}
		}
		s.published[resKey] = account.summed(s.published[resKey])
		trace.Emit(s.ctx, trace.DomainResolve, "publish_selection", resKey,
			trace.Int("symbols", len(symbols)),
			trace.Int("installed", account.installed),
			trace.Int("moved", len(account.moved)),
			trace.Int("unopened", len(account.unopened)),
			trace.Int("uncaptured", len(account.uncaptured)),
			trace.Int("refused", len(account.refused)),
			trace.Int64("duration_ms", time.Since(pubStart).Milliseconds()),
		)
	}
}
