package util

import (
	"github.com/i96751414/libtorrent-go"
)

var (
	Version = "development"
)

func GetVersion() string {
	return Version
}

func UserAgent() string {
	return "torrest/" + GetVersion() + " libtorrent/" + libtorrent.Version()
}
