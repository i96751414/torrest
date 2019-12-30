package util

import (
	"fmt"

	"github.com/i96751414/libtorrent-go"
)

var (
	Version string
)

func GetVersion() string {
	return Version
}

func UserAgent() string {
	return fmt.Sprintf("torrest/%s libtorrent/%s", GetVersion(), libtorrent.Version())
}
