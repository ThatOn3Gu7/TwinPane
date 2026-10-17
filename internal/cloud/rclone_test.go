package cloud

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
	"github.com/ThatOn3Gu7/TwinPane/internal/proc"
)

// Integration tests with the real rclone program (skipped when it is not
// installed). The remotes are of the "local" type (a folder seen through
// rclone) and the "alias" type, in a temporary configuration file: real
// rclone behaviour without network access or credentials. They do NOT
// prove anything about a particular cloud provider.

func fsysOpts(hidden bool) fsys.Options { return fsys.Options{ShowHidden: hidden} }

type bed struct {
	c          *Client
	root       string // local scratch folder
	cloud      string // folder behind the "box" alias remote
	box, other Path
}

func realBed(t *testing.T) *bed {
	t.Helper()
	bin, err := proc.LookPath("rclone")
	if err != nil {
		if os.Getenv("TWINPANE_REQUIRE_TOOLS") != "" {
			t.Fatal("rclone is not installed, and TWINPANE_REQUIRE_TOOLS is set")
		}
		t.Skip("rclone is not installed (the real-rclone tests need it)")
	}
	root := t.TempDir()
	cloud := filepath.Join(root, "cloud")
	other := filepath.Join(root, "other")
	for _, d := range []string{cloud, other, filepath.Join(root, "local")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	conf := filepath.Join(root, "rclone.conf")
	body := "[box]\ntype = alias\nremote = " + cloud + "\n\n[second box]\ntype = alias\nremote = " + other + "\n\n[disk]\ntype = local\n"
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &Client{Bin: bin, Env: []string{"RCLONE_CONFIG=" + conf, "RCLONE_CACHE_DIR=" + filepath.Join(root, "cache")}}
	return &bed{c: c, root: root, cloud: cloud, box: On("box", ""), other: On("second box", "")}
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func tree(t *testing.T, dir string) string {
	t.Helper()
	var out []string
	filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || p == dir {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if fi.IsDir() {
			rel += "/"
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(out)
	return strings.Join(out, " ")
}

func TestRealRemotesAndList(t *testing.T) {
	b := realBed(t)
	ctx := context.Background()
	rs, err := b.c.Remotes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 3 || rs[0] != (Remote{"box", "alias"}) || rs[1] != (Remote{"second box", "alias"}) || rs[2] != (Remote{"disk", "local"}) {
		t.Fatalf("remotes %v", rs)
	}
	write(t, filepath.Join(b.cloud, "dir one", "f #1 [x] *.txt"), "hi")
	write(t, filepath.Join(b.cloud, "-dash"), "x")
	write(t, filepath.Join(b.cloud, ".hidden"), "y")
	l, err := b.c.List(ctx, b.box, fsysOpts(false))
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 2 || l.Entries[0].Name != "dir one" || l.Entries[1].Name != "-dash" || l.Hidden != 1 {
		t.Fatalf("%+v", l)
	}
	l, err = b.c.List(ctx, b.box.Join("dir one"), fsysOpts(false))
	if err != nil || len(l.Entries) != 2 || !l.Entries[0].IsParent || l.Entries[1].Name != "f #1 [x] *.txt" || l.Entries[1].Size != 2 {
		t.Fatalf("%+v %v", l, err)
	}
	if _, err := b.c.List(ctx, b.box.Join("missing"), fsysOpts(false)); KindOf(err) != KindNotFound {
		t.Fatalf("missing folder: %v", err)
	}
	if _, err := b.c.List(ctx, On("nosuchremote", ""), fsysOpts(false)); KindOf(err) != KindNoRemote {
		t.Fatalf("unknown remote: %v", err)
	}
	// a local-type remote uses absolute paths
	l, err = b.c.List(ctx, On("disk", b.cloud), fsysOpts(true))
	if err != nil || len(l.Entries) != 4 {
		t.Fatalf("local-type remote: %+v %v", l.Entries, err)
	}
}

func TestRealMkdirRenameDelete(t *testing.T) {
	b := realBed(t)
	ctx := context.Background()
	if err := b.c.Mkdir(ctx, b.box, "New #1 [a]"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(b.cloud, "New #1 [a]")); err != nil || !fi.IsDir() {
		t.Fatal("folder not created")
	}
	if err := b.c.Mkdir(ctx, b.box, "New #1 [a]"); KindOf(err) != KindExists {
		t.Fatalf("existing folder: %v", err)
	}
	if err := b.c.Mkdir(ctx, b.box, "a/b"); KindOf(err) != KindInvalid {
		t.Fatalf("slash: %v", err)
	}
	write(t, filepath.Join(b.cloud, "old name.txt"), "data")
	write(t, filepath.Join(b.cloud, "taken"), "keep me")
	if err := b.c.Rename(ctx, b.box.Join("old name.txt"), "taken"); KindOf(err) != KindExists {
		t.Fatalf("rename onto an existing name: %v", err)
	}
	if read(t, filepath.Join(b.cloud, "taken")) != "keep me" {
		t.Fatal("existing file was replaced")
	}
	if err := b.c.Rename(ctx, b.box.Join("old name.txt"), "-new name.txt"); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(b.cloud, "-new name.txt")) != "data" {
		t.Fatal("rename")
	}
	if err := b.c.Rename(ctx, b.box.Join("New #1 [a]"), "Folder"); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(b.cloud, "Folder", "in", "x"), "x")
	if err := b.c.Delete(ctx, b.box.Join("Folder"), true); err != nil {
		t.Fatal(err)
	}
	if err := b.c.Delete(ctx, b.box.Join("-new name.txt"), false); err != nil {
		t.Fatal(err)
	}
	if got := tree(t, b.cloud); got != "taken" {
		t.Fatalf("left: %q", got)
	}
	if err := b.c.Delete(ctx, b.box.Join("gone"), false); KindOf(err) != KindNotFound {
		t.Fatalf("delete missing: %v", err)
	}
	if err := b.c.Delete(ctx, b.box, true); KindOf(err) != KindInvalid {
		t.Fatalf("delete root: %v", err)
	}
}

// answers returns a Conflict hook that answers from a list (and records
// what was asked).
func answers(asked *[]string, acts ...ops.Action) func(ops.Conflict) ops.Decision {
	i := 0
	return func(c ops.Conflict) ops.Decision {
		*asked = append(*asked, filepath.Base(c.Dst))
		a := ops.Skip
		if i < len(acts) {
			a = acts[i]
		}
		i++
		return ops.Decision{Action: a}
	}
}

func TestRealTransfers(t *testing.T) {
	b := realBed(t)
	ctx := context.Background()
	local := filepath.Join(b.root, "local")
	write(t, filepath.Join(local, "a [1] *.txt"), "A")
	write(t, filepath.Join(local, "dir", "sub", "b"), "B")
	if err := os.MkdirAll(filepath.Join(local, "dir", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	var progress []ops.Progress
	hooks := ops.Hooks{Progress: func(p ops.Progress) { progress = append(progress, p) }}

	// upload: local → remote
	res := b.c.Transfer(ctx, TransferRequest{Kind: ops.Copy, Dest: b.box, Items: []TransferItem{
		{Src: Local(filepath.Join(local, "a [1] *.txt")), Size: 1},
		{Src: Local(filepath.Join(local, "dir")), IsDir: true},
	}}, hooks)
	if !res.OK() || res.Files != 2 || res.Dirs != 1 || res.Bytes != 2 {
		t.Fatalf("upload: %+v", res)
	}
	if got := tree(t, b.cloud); got != "a [1] *.txt dir/ dir/empty/ dir/sub/ dir/sub/b" {
		t.Fatalf("uploaded tree %q", got)
	}
	if len(progress) == 0 || progress[len(progress)-1].TotalItems != 2 {
		t.Fatalf("progress %+v", progress)
	}

	// remote → remote (two remotes), then move back and forth
	res = b.c.Transfer(ctx, TransferRequest{Kind: ops.Copy, Dest: b.other, Items: []TransferItem{{Src: b.box.Join("dir"), IsDir: true}}}, ops.Hooks{})
	if !res.OK() || tree(t, filepath.Join(b.root, "other")) != "dir/ dir/empty/ dir/sub/ dir/sub/b" {
		t.Fatalf("remote→remote: %+v %q", res, tree(t, filepath.Join(b.root, "other")))
	}
	down := filepath.Join(b.root, "down")
	os.MkdirAll(down, 0o755)
	res = b.c.Transfer(ctx, TransferRequest{Kind: ops.Move, Dest: Local(down), Items: []TransferItem{
		{Src: b.box.Join("a [1] *.txt"), Size: 1}, {Src: b.box.Join("dir"), IsDir: true},
	}}, ops.Hooks{})
	if !res.OK() || res.Files != 2 || tree(t, down) != "a [1] *.txt dir/ dir/empty/ dir/sub/ dir/sub/b" || tree(t, b.cloud) != "" {
		t.Fatalf("download move: %+v down=%q cloud=%q", res, tree(t, down), tree(t, b.cloud))
	}
	// moving within one remote (server-side) still reports the files
	res = b.c.Transfer(ctx, TransferRequest{Kind: ops.Copy, Dest: b.box, Items: []TransferItem{{Src: Local(filepath.Join(down, "dir")), IsDir: true}}}, ops.Hooks{})
	if !res.OK() {
		t.Fatalf("%+v", res)
	}
	b.c.Mkdir(ctx, b.box, "target")
	res = b.c.Transfer(ctx, TransferRequest{Kind: ops.Move, Dest: b.box.Join("target"), Items: []TransferItem{{Src: b.box.Join("dir"), IsDir: true}}}, ops.Hooks{})
	if !res.OK() || res.Files != 1 || res.Dirs != 1 || tree(t, b.cloud) != "target/ target/dir/ target/dir/empty/ target/dir/sub/ target/dir/sub/b" {
		t.Fatalf("server-side move: %+v %q", res, tree(t, b.cloud))
	}

	// invalid requests
	res = b.c.Transfer(ctx, TransferRequest{Kind: ops.Copy, Dest: b.box.Join("target").Join("dir").Join("sub"), Items: []TransferItem{{Src: b.box.Join("target"), IsDir: true}}}, ops.Hooks{})
	if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "into itself") {
		t.Fatalf("into itself: %+v", res)
	}
	res = b.c.Transfer(ctx, TransferRequest{Kind: ops.Copy, Dest: b.box, Items: []TransferItem{{Src: b.box.Join("target"), IsDir: true}}}, ops.Hooks{})
	if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "already in the destination") {
		t.Fatalf("same folder: %+v", res)
	}
	res = b.c.Transfer(ctx, TransferRequest{Kind: ops.Copy, Dest: b.box.Join("nope"), Items: []TransferItem{{Src: Local(filepath.Join(down, "a [1] *.txt"))}}}, ops.Hooks{})
	if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "no longer exists") {
		t.Fatalf("missing destination: %+v", res)
	}
}

func TestRealConflicts(t *testing.T) {
	b := realBed(t)
	ctx := context.Background()
	local := filepath.Join(b.root, "local")
	write(t, filepath.Join(local, "f.txt"), "NEW")
	write(t, filepath.Join(local, "d", "same1"), "new1")
	write(t, filepath.Join(local, "d", "same2"), "new2")
	write(t, filepath.Join(local, "d", "only-new"), "n")
	write(t, filepath.Join(local, "clash"), "file")
	write(t, filepath.Join(b.cloud, "f.txt"), "OLD")
	write(t, filepath.Join(b.cloud, "d", "same1"), "old1")
	write(t, filepath.Join(b.cloud, "d", "same2"), "old2")
	write(t, filepath.Join(b.cloud, "d", "only-old"), "o")
	write(t, filepath.Join(b.cloud, "clash", "x"), "folder")
	// identical size and time: an approved overwrite must still replace it
	now := time.Now().Truncate(time.Second)
	os.Chtimes(filepath.Join(local, "f.txt"), now, now)
	os.Chtimes(filepath.Join(b.cloud, "f.txt"), now, now)

	items := []TransferItem{{Src: Local(filepath.Join(local, "f.txt")), Size: 3}, {Src: Local(filepath.Join(local, "d")), IsDir: true}}

	// no hook: everything that exists is skipped, nothing replaced
	res := b.c.Transfer(ctx, TransferRequest{Kind: ops.Copy, Dest: b.box, Items: items}, ops.Hooks{})
	if !res.OK() || res.Skipped != 2 || read(t, filepath.Join(b.cloud, "f.txt")) != "OLD" {
		t.Fatalf("nil hook: %+v", res)
	}

	// overwrite the file; merge the folder replacing same1 but keeping same2
	var asked []string
	res = b.c.Transfer(ctx, TransferRequest{Kind: ops.Copy, Dest: b.box, Items: items},
		ops.Hooks{Conflict: answers(&asked, ops.Overwrite, ops.Overwrite, ops.Overwrite, ops.Skip)})
	if !res.OK() {
		t.Fatalf("%+v", res)
	}
	if strings.Join(asked, ",") != "f.txt,d,same1,same2" {
		t.Fatalf("asked %q", asked)
	}
	if read(t, filepath.Join(b.cloud, "f.txt")) != "NEW" || read(t, filepath.Join(b.cloud, "d", "same1")) != "new1" ||
		read(t, filepath.Join(b.cloud, "d", "same2")) != "old2" || read(t, filepath.Join(b.cloud, "d", "only-new")) != "n" ||
		read(t, filepath.Join(b.cloud, "d", "only-old")) != "o" {
		t.Fatalf("merge result: %q", tree(t, b.cloud))
	}
	if res.Skipped != 1 || res.Files != 3 {
		t.Fatalf("counts %+v", res)
	}

	// a merge move keeps what was skipped in the source
	asked = nil
	res = b.c.Transfer(ctx, TransferRequest{Kind: ops.Move, Dest: b.box, Items: []TransferItem{{Src: Local(filepath.Join(local, "d")), IsDir: true}}},
		ops.Hooks{Conflict: answers(&asked, ops.Overwrite, ops.Overwrite, ops.Skip, ops.Overwrite)})
	// asked: d (merge), then only-new, same1, same2 (all exist there now)
	if strings.Join(asked, ",") != "d,only-new,same1,same2" {
		t.Fatalf("asked %q", asked)
	}
	if read(t, filepath.Join(b.cloud, "d", "same2")) != "new2" || read(t, filepath.Join(b.cloud, "d", "same1")) != "new1" {
		t.Fatal("approved replacement not moved")
	}
	if !res.OK() || res.Skipped != 1 || res.Files != 2 || tree(t, filepath.Join(local, "d")) != "same1" {
		t.Fatalf("merge move: %+v src=%q", res, tree(t, filepath.Join(local, "d")))
	}

	asked = nil
	// a file onto a folder of the same name is an error, not an overwrite
	res = b.c.Transfer(ctx, TransferRequest{Kind: ops.Copy, Dest: b.box, Items: []TransferItem{{Src: Local(filepath.Join(local, "clash"))}}},
		ops.Hooks{Conflict: answers(&asked, ops.Overwrite)})
	if len(res.Errors) != 1 || read(t, filepath.Join(b.cloud, "clash", "x")) != "folder" {
		t.Fatalf("type clash: %+v", res)
	}

	// cancel at a conflict: nothing more happens
	res = b.c.Transfer(ctx, TransferRequest{Kind: ops.Copy, Dest: b.box, Items: items}, ops.Hooks{Conflict: answers(&asked, ops.Cancel)})
	if !res.Canceled || res.Files != 0 {
		t.Fatalf("cancel: %+v", res)
	}
}

func TestRealTransferCancel(t *testing.T) {
	b := realBed(t)
	local := filepath.Join(b.root, "local")
	big := filepath.Join(local, "big.bin")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	for i := 0; i < 64; i++ {
		f.Write(chunk)
	}
	f.Close()
	// throttle so that the copy is still running when it is cancelled
	b.c.Env = append(b.c.Env, "RCLONE_BWLIMIT=4M")
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 1)
	hooks := ops.Hooks{Progress: func(p ops.Progress) {
		if p.DoneBytes > 0 {
			select {
			case started <- struct{}{}:
			default:
			}
		}
	}}
	done := make(chan ops.Result, 1)
	go func() {
		done <- b.c.Transfer(ctx, TransferRequest{Kind: ops.Copy, Dest: b.box, Items: []TransferItem{{Src: Local(big), Size: 64 << 20}}}, hooks)
	}()
	select {
	case <-started:
	case <-time.After(20 * time.Second):
		t.Fatal("the transfer never reported progress")
	}
	cancel()
	var res ops.Result
	select {
	case res = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("cancel did not stop the transfer")
	}
	if !res.Canceled || res.Files != 0 {
		t.Fatalf("%+v", res)
	}
	// rclone was interrupted (not killed) and removed its partial file
	if got := tree(t, b.cloud); got != "" {
		t.Fatalf("left in the destination: %q", got)
	}
	if _, err := os.Stat(big); err != nil {
		t.Fatal("source damaged")
	}
}
