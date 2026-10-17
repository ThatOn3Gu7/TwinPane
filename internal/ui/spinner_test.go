package ui

import (
	"strings"
	"testing"
	"time"
)

// spinnerFrame returns the spinner character shown on the screen ("" if
// none). The frames are the braille dots of panel's spinner.
func spinnerFrame(view string) string {
	for _, f := range []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"} {
		if strings.Contains(view, f) {
			return f
		}
	}
	return ""
}

// SIMULATED: rclone takes about 1.5 s to list a folder. While it does, the
// panel shows an animated spinner with the folder's name, the seconds and
// the Esc hint; the spinner disappears as soon as the listing arrives, and
// the ticks stop, so an idle TwinPane does no work.
func TestSpinnerDuringSlowRemoteListing(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	fakeRclone(t, `for a in "$@"; do
  case "$a" in
    listremotes) echo "slow:         drive"; exit 0 ;;
    lsjson) sleep 1.5
      echo '[{"Path":"report.txt","Name":"report.txt","Size":12,"ModTime":"2026-01-02T03:04:05Z","IsDir":false}]'
      exit 0 ;;
  esac
done
exit 1
`)
	// the start-up loads are done: their last tick finds nothing loading
	h.until("start-up ticks to stop", func() bool { return !h.a.spinning })
	h.press("ctrl+r")
	h.waitDialog(dlgSources)
	h.until("remotes", func() bool { return strings.Contains(h.view(), "slow:") })
	h.press("1")
	p := h.a.ActivePanel()
	if !p.Loading() {
		t.Fatal("the remote should be loading")
	}

	frames := map[string]bool{}
	sawLine, sawSeconds := false, false
	h.until("listing", func() bool {
		v := h.view()
		if p.Loading() {
			if f := spinnerFrame(v); f != "" {
				frames[f] = true
			}
			sawLine = sawLine || strings.Contains(v, " Loading slow: / … ") && strings.Contains(v, "Esc cancels")
			sawSeconds = sawSeconds || strings.Contains(v, "slow: / … 1s · Esc cancels")
		}
		return !p.Loading()
	})
	if len(frames) < 3 {
		t.Fatalf("the spinner should animate while loading; frames seen: %v", frames)
	}
	if !sawLine || !sawSeconds {
		t.Fatalf("the info line should name the folder, count seconds and offer Esc (line=%v seconds=%v)", sawLine, sawSeconds)
	}

	v := h.view()
	if f := spinnerFrame(v); f != "" || strings.Contains(v, " Loading slow:") {
		t.Fatalf("spinner must vanish once the folder is listed:\n%s", v)
	}
	if !p.InRemote() || !strings.Contains(v, "report.txt") {
		t.Fatalf("listing not shown:\n%s", v)
	}
	// the one tick still in flight finds nothing loading and is not renewed
	h.until("ticks to stop", func() bool { return !h.a.spinning })
	time.Sleep(250 * time.Millisecond)
	h.settle()
	if h.a.spinning {
		t.Fatal("spinner ticks continue after loading finished")
	}
}

// Cancelling a slow listing with Esc removes the spinner too.
func TestSpinnerStopsWhenListingCancelled(t *testing.T) { // SIMULATED
	h, _ := newOpsApp(t, nil, nil)
	fakeRclone(t, `for a in "$@"; do
  case "$a" in
    listremotes) echo "slow:         drive"; exit 0 ;;
    lsjson) exec sleep 60 ;;
  esac
done
exit 1
`)
	h.press("ctrl+r")
	h.waitDialog(dlgSources)
	h.until("remotes", func() bool { return strings.Contains(h.view(), "slow:") })
	h.press("1")
	p := h.a.ActivePanel()
	h.until("spinner animating", func() bool { return spinnerFrame(h.view()) != "" && h.a.spinning && p.Loading() })
	h.press("esc")
	h.until("cancelled", func() bool { return !p.Loading() })
	if f := spinnerFrame(h.view()); f != "" {
		t.Fatalf("spinner left after cancelling:\n%s", h.view())
	}
	h.until("ticks to stop", func() bool { return !h.a.spinning })
}
