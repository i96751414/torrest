package bittorrent

import (
	"io"
	"time"
)

const (
	piecesRefreshDuration = 500 * time.Millisecond
	waitForPieceTimeout   = 60 * time.Second
)

type Reader interface {
	io.Reader
	io.Seeker
	io.Closer
}

type reader struct {
	storage         Reader
	torrent         *Torrent
	offset          int64
	length          int64
	pieceLength     int64
	priorityPieces  int
	closing         chan interface{}
	defaultPriority *uint
	currentPiece    int
	closeNotifiers  []<-chan bool
}

func (r *reader) RegisterCloseNotifier(n <-chan bool) {
	r.closeNotifiers = append(r.closeNotifiers, n)
}

func (r *reader) waitForPiece(piece int, timeout time.Duration) error {
	log.Debugf("Waiting for piece %d", piece)

	startTime := time.Now()
	pieceRefreshTicker := time.NewTicker(piecesRefreshDuration)
	defer pieceRefreshTicker.Stop()

	for !r.torrent.handle.HavePiece(piece) {
		select {
		case <-r.torrent.service.closing:
			return ServiceClosedError
		case <-r.torrent.closing:
			return TorrentClosedError
		case <-r.closing:
			return ReaderClosedError
		case <-pieceRefreshTicker.C:
			if timeout != 0 && time.Since(startTime) >= timeout {
				log.Warningf("Timed out waiting for piece %d with priority %v",
					piece, r.torrent.handle.PiecePriority(piece))
				return TimeoutError
			}

			for _, n := range r.closeNotifiers {
				select {
				case <-n:
					return ReaderCloseNotifyError
				default:
					// do nothing
				}
			}
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

	lastPiece := r.pieceFromOffset(currentOffset + int64(len(b)))
	for p := r.pieceFromOffset(currentOffset); p <= lastPiece; p++ {
		if !r.torrent.handle.HavePiece(p) {
			// Try to avoid setting piece priorities multiple times
			if p != r.currentPiece {
				r.setPiecesPriorities(p)
			}
			if err := r.waitForPiece(p, waitForPieceTimeout); err != nil {
				return 0, err
			}
		}
		// Prepare for the next piece
		if p == r.currentPiece {
			r.currentPiece++
		}
	}

	return r.storage.Read(b)
}

func (r *reader) Close() error {
	close(r.closing)
	return r.storage.Close()
}

func (r *reader) setPiecesPriorities(piece int) {
	log.Debugf("We don't have piece %d, setting piece priorities", piece)
	r.currentPiece = piece
	p := r.pieceFromOffset(0)
	deadline := 0
	for ; p < piece; p++ {
		r.torrent.handle.PiecePriority(p, *r.defaultPriority)
		r.torrent.handle.ResetPieceDeadline(p)
	}
	for ; p <= piece+r.priorityPieces; p, deadline = p+1, deadline+1 {
		r.torrent.handle.PiecePriority(p, TopPriority)
		r.torrent.handle.SetPieceDeadline(p, deadline)
	}
	for ; p <= r.pieceFromOffset(r.length); p++ {
		r.torrent.handle.PiecePriority(p, HighPriority)
		r.torrent.handle.ResetPieceDeadline(p)
	}
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
		r.setPiecesPriorities(piece)
	}

	return r.storage.Seek(off, whence)
}
