package witnesscache

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/greatliontech/stipulator/internal/recordstore"
)

// Install atomically writes one record's variant file and bounds the
// identity's variant set: beyond variantBound, the least recently
// installed variants are evicted — eviction costs only execution.
func Install(ctx context.Context, dir string, rec Record) error {
	_, errs, ctxErr := InstallBatch(ctx, dir, []Record{rec})
	if ctxErr != nil {
		return ctxErr
	}
	return errs[0]
}

// InstallBatch validates and marshals each record independently before acquiring
// the store lock, persists each distinct valid compartment ledger once, writes
// valid records atomically while isolating per-record failures, and runs a
// single variant-eviction scan across all landed records.
func InstallBatch(ctx context.Context, dir string, records []Record) ([]Record, []error, error) {
	if len(records) == 0 {
		return nil, nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	errs := make([]error, len(records))
	store, openErr := open(dir)
	if openErr != nil {
		for i := range records {
			errs[i] = openErr
		}
		return nil, errs, ctx.Err()
	}
	type preparedRecord struct {
		idx      int
		rec      Record
		name     string
		data     []byte
		coordKey string
	}
	valid := make([]preparedRecord, 0, len(records))
	for i, rec := range records {
		if err := ctx.Err(); err != nil {
			return nil, errs, err
		}
		name, err := fileName(rec)
		if err != nil {
			errs[i] = err
			continue
		}
		var coordKey string
		if rec.CompartmentLedger != nil {
			coordKey = coordinateOf(rec).key()
			if coordKey == "" || !validLedger(rec.CompartmentLedger) {
				errs[i] = fmt.Errorf("witnesscache: incomplete ledger coordinate or binding evidence for %s", rec.Key())
				continue
			}
		}
		e := entry{
			Version:               version,
			Group:                 rec.Group,
			Package:               rec.Package,
			Test:                  rec.Test,
			Fingerprint:           rec.Fingerprint,
			Outcomes:              rec.Outcomes,
			Regs:                  rec.Regs,
			ObservationExclusions: rec.ObservationExclusions,
			ObservationNamespaces: rec.ObservationNamespaces,
		}
		data, err := json.MarshalIndent(e, "", "  ")
		if err != nil {
			errs[i] = err
			continue
		}
		valid = append(valid, preparedRecord{idx: i, rec: rec, name: name, data: data, coordKey: coordKey})
	}
	if len(valid) == 0 {
		return nil, errs, ctx.Err()
	}
	lock, err := store.Lock(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, errs, ctxErr
		}
		for _, v := range valid {
			errs[v.idx] = err
		}
		return nil, errs, nil
	}
	defer lock.Close()

	ledgerErrByCoord := map[string]error{}
	for _, v := range valid {
		if v.coordKey == "" {
			continue
		}
		if _, seen := ledgerErrByCoord[v.coordKey]; seen {
			continue
		}
		if err := installLedger(ctx, store, v.rec); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, errs, ctxErr
			}
			ledgerErrByCoord[v.coordKey] = err
			continue
		}
		ledgerErrByCoord[v.coordKey] = nil
	}

	installed := make([]Record, 0, len(valid))
	landedNames := make([]string, 0, len(valid))
	for _, v := range valid {
		if v.coordKey != "" {
			if lErr := ledgerErrByCoord[v.coordKey]; lErr != nil {
				errs[v.idx] = lErr
				continue
			}
		}
		if err := ctx.Err(); err != nil {
			if len(landedNames) > 0 {
				_ = store.BoundVariants(variantBound, landedNames)
			}
			return installed, errs, err
		}
		if err := store.Write(v.name, v.data); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				if len(landedNames) > 0 {
					_ = store.BoundVariants(variantBound, landedNames)
				}
				return installed, errs, ctxErr
			}
			errs[v.idx] = err
			continue
		}
		installed = append(installed, v.rec)
		landedNames = append(landedNames, v.name)
	}
	if len(landedNames) > 0 {
		_ = store.BoundVariants(variantBound, landedNames)
	}
	return installed, errs, ctx.Err()
}

// PruneGlobal performs global usage/preview and bounded reclamation across all
// witness stores and their ledger substores under the user cache directory.
func PruneGlobal(ctx context.Context, opts recordstore.PruneOptions) (recordstore.PruneReport, error) {
	return recordstore.PruneKind(ctx, "witnesses", opts, func(ctx context.Context, s recordstore.Store, abandoned, dryRun bool) (used, reclaimed int64, removed, skipped int, onEvict recordstore.RecordEvictHook, err error) {
		ls := ledgerStore(s)
		entries, readErr := os.ReadDir(ls.Path())
		if os.IsNotExist(readErr) {
			return 0, 0, 0, 0, nil, nil
		}
		if readErr != nil {
			return 0, 0, 0, 1, nil, readErr
		}
		refCount := map[string]int{}
		refsByRecord := map[string][]string{}
		if !abandoned {
			names, _ := s.Names()
			for _, name := range names {
				if cancelErr := ctx.Err(); cancelErr != nil {
					return 0, 0, 0, 0, nil, cancelErr
				}
				data, err := s.Read(name)
				if err != nil {
					continue
				}
				rec, digest, ok := decodeRecord(name, data)
				var keys []string
				if digest != "" {
					keys = append(keys, digest)
				}
				if ok {
					if coordKey := coordinateOf(rec).key(); coordKey != "" && coordKey != digest {
						keys = append(keys, coordKey)
					}
				}
				if len(keys) > 0 {
					refsByRecord[name] = keys
					for _, k := range keys {
						refCount[k]++
					}
				}
			}
		}
		liveLedgerSize := map[string]int64{}
		liveLedgerPath := map[string]string{}
		for _, e := range entries {
			if cancelErr := ctx.Err(); cancelErr != nil {
				return used, reclaimed, removed, skipped, nil, cancelErr
			}
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			info, infoErr := e.Info()
			if infoErr != nil {
				if !os.IsNotExist(infoErr) {
					skipped++
				}
				continue
			}
			size := info.Size()
			used += size
			key := strings.TrimSuffix(e.Name(), ".json")
			full := filepath.Join(ls.Path(), e.Name())
			if !abandoned && refCount[key] > 0 {
				liveLedgerSize[key] = size
				liveLedgerPath[key] = full
				continue
			}
			if dryRun {
				reclaimed += size
				removed++
				continue
			}
			if rmErr := os.Remove(full); rmErr != nil && !os.IsNotExist(rmErr) {
				skipped++
				continue
			}
			reclaimed += size
			removed++
		}
		if abandoned && !dryRun {
			_ = os.Remove(ls.Path())
		}
		if !abandoned && len(liveLedgerSize) > 0 {
			onEvict = func(ctx context.Context, recordName string, dryRun bool) (evictReclaimed int64, evictRemoved, evictSkipped int, evictErr error) {
				keys := refsByRecord[recordName]
				if len(keys) == 0 {
					return 0, 0, 0, nil
				}
				delete(refsByRecord, recordName)
				for _, k := range keys {
					refCount[k]--
					if refCount[k] > 0 {
						continue
					}
					full, ok := liveLedgerPath[k]
					if !ok {
						continue
					}
					if cancelErr := ctx.Err(); cancelErr != nil {
						return evictReclaimed, evictRemoved, evictSkipped, cancelErr
					}
					size := liveLedgerSize[k]
					delete(liveLedgerPath, k)
					delete(liveLedgerSize, k)
					if !dryRun {
						if rmErr := os.Remove(full); rmErr != nil && !os.IsNotExist(rmErr) {
							evictSkipped++
							continue
						}
					}
					evictReclaimed += size
					evictRemoved++
				}
				return evictReclaimed, evictRemoved, evictSkipped, nil
			}
		}
		return used, reclaimed, removed, skipped, onEvict, nil
	})
}
