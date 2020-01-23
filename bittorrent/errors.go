package bittorrent

import "errors"

var (
	DuplicateTorrentError  = errors.New("torrent was previously added")
	LoadTorrentError       = errors.New("failed loading torrent")
	InvalidInfoHashError   = errors.New("no such info hash")
	InvalidFileIdError     = errors.New("no such file id")
	ServiceClosedError     = errors.New("service was closed")
	TorrentClosedError     = errors.New("torrent was closed")
	ReaderClosedError      = errors.New("reader was closed")
	ReaderCloseNotifyError = errors.New("reader close notify received")
	InvalidWhenceError     = errors.New("invalid whence")
	TimeoutError           = errors.New("timeout reached")
)
