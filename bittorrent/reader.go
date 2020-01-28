package bittorrent

import (
	"io"
	"time"
)

const (
	piecesRefreshDuration = 500 * time.Millisecond
	waitForPieceTimeout   = 60 * time.Second
)

type Storage interface {
	io.Reader
	io.Seeker
	io.Closer
}

type reader struct {
	storage        Storage
	torrent        *Torrent
	offset         int64
	length         int64
	pieceLength    int64
	priorityPieces int
	closing        chan interface{}
	firstPiece     int
	lastPiece      int
	closeNotifiers []<-chan bool
}

func newReader(storage Storage, torrent *Torrent, offset, length, pieceLength int64, readAhead float64) *reader {
	r := &reader{
		storage:        storage,
		torrent:        torrent,
		offset:         offset,
		length:         length,
		pieceLength:    pieceLength,
		priorityPieces: int(0.5 + readAhead*float64(length)/float64(pieceLength)),
		closing:        make(chan interface{}),
	}
	r.firstPiece = r.pieceFromOffset(0)
	r.lastPiece = r.pieceFromOffset(length)
	return r
}

func (r *reader) RegisterCloseNotifier(n <-chan bool) {
	r.closeNotifiers = append(r.closeNotifiers, n)
}

func (r *reader) waitForPiece(piece int, timeout time.Duration) error {
	log.Debugf("Waiting for piece %d on '%s'", piece, r.torrent.infoHash)

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
				log.Warningf("Timed out waiting for piece %d with priority %v for '%s'",
					piece, r.torrent.handle.PiecePriority(piece), r.torrent.infoHash)
				return TimeoutError
			}

			for _, n := range r.closeNotifiers {
				select {
				case <-n:
					log.Debug("Received close notify for '%s'", r.torrent.infoHash)
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

	startPiece := r.pieceFromOffset(currentOffset)
	endPiece := r.pieceFromOffset(currentOffset + int64(len(b)))
	r.setPiecesPriorities(startPiece, endPiece-startPiece)
	for p := startPiece; p <= endPiece; p++ {
		if !r.torrent.handle.HavePiece(p) {
			if err := r.waitForPiece(p, waitForPieceTimeout); err != nil {
				return 0, err
			}
		}
	}

	return r.storage.Read(b)
}

func (r *reader) Close() error {
	log.Debugf("Closing reader for '%s'", r.torrent.infoHash)
	close(r.closing)
	return r.storage.Close()
}

func (r *reader) setPiecePriority(piece int, deadline int, priority uint) {
	if r.torrent.handle.PiecePriority(piece).(uint) < priority {
		r.torrent.handle.PiecePriority(piece, priority)
		r.torrent.handle.SetPieceDeadline(piece, deadline)
	}
}

func (r *reader) setPiecesPriorities(piece int, pieceEndOffset int) {
	endPiece := piece + pieceEndOffset + r.priorityPieces
	for p, i := piece, 0; p <= endPiece && p <= r.lastPiece; p, i = p+1, i+1 {
		if !r.torrent.handle.HavePiece(p) {
			if i <= pieceEndOffset {
				r.setPiecePriority(p, 0, TopPriority)
			} else {
				r.setPiecePriority(p, (i-pieceEndOffset)*10, HighPriority)
			}
		}
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

	r.setPiecesPriorities(r.pieceFromOffset(seekingOffset), 0)
	return r.storage.Seek(off, whence)
}
