// +build !darwin,!freebsd,!dragonfly

package bittorrent

import "os"

func unlockFile(_ *os.File) error {
	return nil
}
