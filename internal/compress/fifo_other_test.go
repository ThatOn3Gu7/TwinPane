//go:build !unix

package compress

import "errors"

func mkfifo(string) error { return errors.New("no FIFOs on this platform") }
