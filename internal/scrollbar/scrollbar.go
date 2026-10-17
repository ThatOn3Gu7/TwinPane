// Package scrollbar computes the geometry of slim, text-mode scroll
// indicators. It is shared by the file panels, the search results and the
// bulk-rename list so that every list in TwinPane reports its position the
// same way.
package scrollbar

// Thumb returns where the thumb of a vertical scroll indicator sits on a
// track of `track` cells for a list of `total` items of which `visible` are
// shown starting at `offset`.
//
// ok is false when nothing needs indicating (everything fits, or the track
// is empty); callers then draw no indicator at all. The thumb length is
// proportional to visible/total (at least one cell) and the thumb touches
// the top of the track exactly when offset is 0 and the bottom exactly when
// the last item is visible.
func Thumb(total, visible, offset, track int) (start, size int, ok bool) {
	if track <= 0 || visible <= 0 || total <= visible {
		return 0, 0, false
	}
	maxOff := total - visible
	if offset < 0 {
		offset = 0
	}
	if offset > maxOff {
		offset = maxOff
	}
	size = (track*visible + total/2) / total
	if size < 1 {
		size = 1
	}
	if size >= track {
		// Can only happen for tiny tracks; keep one cell of travel so the
		// indicator still says "there is more".
		size = track - 1
		if size < 1 {
			size = 1
		}
	}
	free := track - size
	switch {
	case offset == 0:
		start = 0
	case offset == maxOff:
		start = free
	default:
		start = (offset*free + maxOff/2) / maxOff
		// Not at an end: never draw the thumb as if it were.
		if start == 0 && free > 1 {
			start = 1
		}
		if start == free && free > 1 {
			start = free - 1
		}
	}
	return start, size, true
}

// OffsetAt converts a position on the track (0-based cell, e.g. where the
// user clicked or dragged) into a list offset. The thumb is centred on the
// pointer where possible; the result is clamped to [0, total-visible].
func OffsetAt(cell, total, visible, track int) int {
	if track <= 0 || total <= visible {
		return 0
	}
	maxOff := total - visible
	if track == 1 {
		return 0
	}
	if cell <= 0 {
		return 0
	}
	if cell >= track-1 {
		return maxOff
	}
	off := (cell*maxOff + (track-1)/2) / (track - 1)
	if off < 0 {
		off = 0
	}
	if off > maxOff {
		off = maxOff
	}
	return off
}

// Cells renders the indicator as one entry per track cell: true where the
// thumb is. It returns nil when no indicator is needed.
func Cells(total, visible, offset, track int) []bool {
	start, size, ok := Thumb(total, visible, offset, track)
	if !ok {
		return nil
	}
	cells := make([]bool, track)
	for i := start; i < start+size && i < track; i++ {
		cells[i] = true
	}
	return cells
}
