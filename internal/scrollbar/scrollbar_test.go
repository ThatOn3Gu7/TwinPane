package scrollbar

import "testing"

func TestNoIndicatorWhenEverythingFits(t *testing.T) {
	for _, c := range [][4]int{{0, 10, 0, 10}, {5, 10, 0, 10}, {10, 10, 0, 10}, {50, 10, 0, 0}, {50, 0, 0, 10}} {
		if _, _, ok := Thumb(c[0], c[1], c[2], c[3]); ok {
			t.Errorf("Thumb%v: want no indicator", c)
		}
		if Cells(c[0], c[1], c[2], c[3]) != nil {
			t.Errorf("Cells%v: want nil", c)
		}
	}
}

func TestThumbSizeIsProportional(t *testing.T) {
	cases := []struct{ total, visible, track, want int }{
		{20, 10, 10, 5}, {100, 10, 10, 1}, {1000, 10, 10, 1}, {40, 30, 30, 23}, {11, 10, 10, 9},
	}
	for _, c := range cases {
		_, size, ok := Thumb(c.total, c.visible, 0, c.track)
		if !ok || size != c.want {
			t.Errorf("total %d visible %d track %d: size %d ok %v, want %d", c.total, c.visible, c.track, size, ok, c.want)
		}
	}
}

func TestThumbPositionEndsAndMonotonic(t *testing.T) {
	total, visible, track := 237, 17, 17
	maxOff := total - visible
	prev := -1
	for off := 0; off <= maxOff; off++ {
		start, size, ok := Thumb(total, visible, off, track)
		if !ok {
			t.Fatal("indicator expected")
		}
		if start < prev {
			t.Fatalf("offset %d: start %d moved backwards from %d", off, start, prev)
		}
		prev = start
		switch {
		case off == 0 && start != 0:
			t.Fatalf("top: start %d", start)
		case off == maxOff && start+size != track:
			t.Fatalf("bottom: start %d size %d", start, size)
		case off > 0 && off < maxOff && (start == 0 || start+size == track):
			t.Fatalf("offset %d (not at an end) drawn at an end: start %d size %d", off, start, size)
		}
	}
	// out-of-range offsets are clamped
	if s, _, _ := Thumb(total, visible, -5, track); s != 0 {
		t.Fatal("negative offset")
	}
	if s, sz, _ := Thumb(total, visible, 1e6, track); s+sz != track {
		t.Fatal("huge offset")
	}
}

func TestTinyTrackStillShowsThereIsMore(t *testing.T) {
	start, size, ok := Thumb(3, 2, 0, 2)
	if !ok || size != 1 || start != 0 {
		t.Fatalf("start %d size %d ok %v", start, size, ok)
	}
	start, size, _ = Thumb(3, 2, 1, 2)
	if start != 1 || size != 1 {
		t.Fatalf("bottom: start %d size %d", start, size)
	}
}

func TestOffsetAt(t *testing.T) {
	total, visible, track := 100, 10, 10
	if got := OffsetAt(0, total, visible, track); got != 0 {
		t.Fatalf("top cell: %d", got)
	}
	if got := OffsetAt(track-1, total, visible, track); got != 90 {
		t.Fatalf("bottom cell: %d", got)
	}
	if got := OffsetAt(-3, total, visible, track); got != 0 {
		t.Fatalf("above: %d", got)
	}
	if got := OffsetAt(99, total, visible, track); got != 90 {
		t.Fatalf("below: %d", got)
	}
	mid := OffsetAt(5, total, visible, track)
	if mid <= 0 || mid >= 90 {
		t.Fatalf("middle: %d", mid)
	}
	// the thumb then sits near the clicked cell
	if s, sz, _ := Thumb(total, visible, mid, track); 5 < s || 5 > s+sz {
		t.Fatalf("thumb %d+%d does not cover cell 5", s, sz)
	}
	if OffsetAt(3, 5, 10, 10) != 0 {
		t.Fatal("fits: offset must be 0")
	}
}
