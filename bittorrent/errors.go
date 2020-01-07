package bittorrent

import "errors"

var (
	DuplicateTorrentError = errors.New("torrent was previously added")
	LoadTorrentError      = errors.New("failed loading torrent")
	InvalidInfoHashError  = errors.New("no such info hash")
	InvalidFileIdError    = errors.New("no such file id")
	TorrentClosedError    = errors.New("torrent was closed")
	FileClosedError       = errors.New("file was closed")
	InvalidWhenceError    = errors.New("invalid whence")
)
