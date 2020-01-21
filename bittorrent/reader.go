package bittorrent

import (
	"io"
	"time"
)

const (
	piecesRefreshDuration = 500 * time.Millisecond
)

type Reader interface {
	io.Reader
	io.Seeker
	io.Closer
}

type reader struct {
	storage        Reader
	torrent        *Torrent
	offset         int64
	length         int64
	pieceLength    int64
	priorityPieces int
	closing        chan interface{}
}

func (r *reader) waitForPiece(piece int) error {
	if r.torrent.handle.HavePiece(piece) {
		return nil
	}

	log.Infof("Waiting for piece %d", piece)

	pieceRefreshTicker := time.NewTicker(piecesRefreshDuration)
	defer pieceRefreshTicker.Stop()

	for !r.torrent.handle.HavePiece(piece) {
		select {
		case <-r.torrent.closing:
			log.Warningf("Unable to wait for piece %d as torrent was closed", piece)
			return TorrentClosedError
		case <-r.closing:
			log.Warningf("Unable to wait for piece %d as file was closed", piece)
			return FileClosedError
		case <-pieceRefreshTicker.C:
			continue
		}
	}
	return nil
}

func (r *reader) pieceFromOffset(offset int64) int {
	return int((r.offset + offset) / r.pieceLength)
}

func (r *reader) pos() (int64, error) {
	return r.storage.Seek(0, io.SeekCurrent)
}

func (r *reader) Read(b []byte) (int, error) {
	currentOffset, err := r.pos()
	if err != nil {
		return 0, err
	}
	// TODO: Check all the pieces
	piece := r.pieceFromOffset(currentOffset + int64(len(b)))
	if err := r.waitForPiece(piece); err != nil {
		return 0, err
	}

	return r.storage.Read(b)
}

func (r *reader) Close() error {
	close(r.closing)
	return r.storage.Close()
}

func (r *reader) Seek(off int64, whence int) (int64, error) {
	seekingOffset := off

	switch whence {
	case io.SeekStart:
		// do nothing
	case io.SeekCurrent:
		if currentOffset, err := r.pos(); err == nil {
			seekingOffset += currentOffset
		} else {
			return currentOffset, err
		}
	case io.SeekEnd:
		seekingOffset = r.length - seekingOffset
	default:
		return -1, InvalidWhenceError
	}

	piece := r.pieceFromOffset(seekingOffset)
	if !r.torrent.handle.HavePiece(piece) {
		log.Infof("We don't have piece %d, setting piece priorities", piece)
		p := r.pieceFromOffset(0)
		for ; p < piece; p++ {
			r.torrent.handle.PiecePriority(p, DontDownloadPriority)
		}
		for ; p <= piece+r.priorityPieces; p++ {
			r.torrent.handle.PiecePriority(p, TopPriority)
			r.torrent.handle.SetPieceDeadline(p, 0)
		}
		for ; p <= r.pieceFromOffset(r.length); p++ {
			r.torrent.handle.PiecePriority(p, HighPriority)
		}
	}

	return r.storage.Seek(off, whence)
}
