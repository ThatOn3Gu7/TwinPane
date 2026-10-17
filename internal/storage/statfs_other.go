//go:build !linux

package storage

func statfs(in *Info) { in.SizeErr = ErrUnsupported }
