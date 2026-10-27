package ops

import (
	"io/fs"
	"path/filepath"
)

// EstimateEntryCap bounds the walk EstimateTransfer performs, so deciding
// whether an operation is large enough to offer a progress choice stays cheap
// even for enormous trees.
const EstimateEntryCap = 4000

// Estimate is a bounded guess of how much work a copy or move represents. It
// only decides whether an operation is worth a progress choice and gives the
// user a rough size; the transfer itself counts exactly while it scans (see
// Progress).
type Estimate struct {
	Files int
	Dirs  int
	Bytes int64 // regular file bytes
	// Truncated reports that the walk stopped at EstimateEntryCap, so the real
	// totals are larger than the ones reported here.
	Truncated bool
}

// Items is the number of entries the estimate walked over.
func (e Estimate) Items() int { return e.Files + e.Dirs }

// EstimateTransfer sums the entries and bytes of a request's sources. Like the
// engine's own scan it never follows symlinks, and unreadable folders are
// skipped (they are reported later, during the transfer). The walk is bounded
// by EstimateEntryCap.
func EstimateTransfer(req Request) Estimate {
	var est Estimate
	for _, src := range req.Sources {
		if est.Items() >= EstimateEntryCap {
			est.Truncated = true
			break
		}
		_ = filepath.WalkDir(filepath.Clean(src), func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				est.Dirs++
			} else {
				est.Files++
				if d.Type().IsRegular() {
					if info, err := d.Info(); err == nil {
						est.Bytes += info.Size()
					}
				}
			}
			if est.Items() >= EstimateEntryCap {
				est.Truncated = true
				return fs.SkipAll
			}
			return nil
		})
	}
	return est
}

// EstimateDelete turns an approved delete plan into an estimate: planning
// already counted the whole tree, so no extra walk is needed.
func EstimateDelete(plan DeletePlan) Estimate {
	return Estimate{
		Files:     plan.Files,
		Dirs:      plan.Dirs,
		Bytes:     plan.Bytes,
		Truncated: plan.Incomplete, // unreadable folders mean the totals are minimums
	}
}
