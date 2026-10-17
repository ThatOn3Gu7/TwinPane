//go:build !unix

package archive

import "errors"

func mkfifo(string) error { return errors.New("no FIFOs here") }
