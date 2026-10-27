package cloud

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// Reading a remote file for the read-only viewer.
//
// The viewer previews remote files by streaming them through the user's own
// rclone and stopping at the viewer's size limit, so a huge file on the
// provider never turns into a huge download. Two rules matter here:
//
//   - the transfer is bounded: at most max bytes are read and rclone is
//     interrupted as soon as the limit is reached, so a backend that keeps
//     sending (or a file whose size is unknown) cannot flood the user's
//     connection or disk;
//   - providers differ: some report a size, some report 0 for files that do
//     have content, some refuse "cat" (server-side documents, unsupported
//     operations) and some fail halfway. Nothing is assumed about the size —
//     the returned error says what rclone reported.

// ErrPreviewTooLarge reports a file above the preview limit; it is returned
// before anything is transferred when the provider knows the size.
var ErrPreviewTooLarge = errors.New("file is too large to preview")

// ReadFile streams p into w, reading at most max bytes.
//
// n is how many bytes were written; truncated is true when rclone had more to
// send (the rest was not transferred). A cancel of ctx stops rclone and is
// reported as a KindCanceled *Error, so callers can tell it from a failure.
func (c *Client) ReadFile(ctx context.Context, p Path, w io.Writer, max int64) (n int64, truncated bool, err error) {
	if c == nil || c.Bin == "" {
		return 0, false, ErrNotInstalled
	}
	if max <= 0 {
		return 0, false, &Error{Op: "read", Kind: KindOther, Remote: p.Remote, Msg: "no read limit was given"}
	}
	if err := ctx.Err(); err != nil {
		return 0, false, &Error{Op: "read", Kind: KindCanceled, Remote: p.Remote, Msg: "cancelled"}
	}
	// Our own cancel: it stops rclone as soon as the limit is reached (see
	// capWriter), while parent cancellation still comes from the caller.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cw := &capWriter{w: w, max: max, cancel: cancel}
	stderr, st, runErr := c.run(runCtx, []string{"cat", "--", p.Arg()}, cw, nil)
	n, truncated = cw.n, cw.over

	switch {
	case ctx.Err() != nil:
		return n, truncated, &Error{Op: "read", Kind: KindCanceled, Remote: p.Remote, Msg: "cancelled"}
	case truncated:
		// rclone was interrupted on purpose: this is a complete, bounded read.
		return n, true, nil
	case errors.Is(runErr, ErrNotInstalled):
		return n, false, ErrNotInstalled
	case runErr != nil && !errors.Is(runErr, context.Canceled):
		return n, false, &Error{Op: "read", Kind: KindOther, Remote: p.Remote, Msg: runErr.Error()}
	case runErr != nil: // cancelled without our limit being reached
		return n, false, &Error{Op: "read", Kind: KindCanceled, Remote: p.Remote, Msg: "cancelled"}
	case !st.Success():
		// rclone said no (server-side document, unsupported operation, a
		// provider that failed midway, …): pass its own explanation on.
		e := explain("read", p.Remote, st, stderr)
		if n > 0 {
			e.Msg = fmt.Sprintf("%s (after %d bytes)", e.Msg, n)
		}
		return n, false, e
	}
	return n, false, nil
}

// capWriter writes at most max bytes and stops rclone when there is more.
// The extra byte tells a read that ended exactly at the limit from one that
// was cut: with rclone, only the latter is "truncated".
type capWriter struct {
	w      io.Writer
	max    int64
	n      int64
	over   bool
	cancel context.CancelFunc
}

func (c *capWriter) Write(b []byte) (int, error) {
	if c.over {
		return 0, io.ErrClosedPipe
	}
	room := c.max - c.n
	if int64(len(b)) > room {
		b = b[:room]
		c.over = true
		// interrupt rclone instead of draining a file we are not showing
		c.cancel()
	}
	if len(b) > 0 {
		written, err := c.w.Write(b)
		c.n += int64(written)
		if err != nil {
			return written, err
		}
	}
	if c.over {
		return len(b), io.ErrClosedPipe
	}
	return len(b), nil
}
