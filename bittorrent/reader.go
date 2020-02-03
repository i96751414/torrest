package bittorrent

import (
	"errors"
	"io"
	"sync"
	"time"

	"github.com/i96751414/libtorrent-go"
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
	mu             *sync.Mutex
	storage        libtorrent.StorageInterface
	torrent        *Torrent
	offset         int64
	length         int64
	pieceLength    int64
	priorityPieces int
	closing        chan interface{}
	firstPiece     int
	lastPiece      int
	closeNotifiers []<-chan bool
	pos            int64
}

func newReader(torrent *Torrent, offset, length, pieceLength int64, readAhead float64) *reader {
	r := &reader{
		mu:             &sync.Mutex{},
		storage:        torrent.handle.GetStorageImpl(),
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

func (r *reader) pieceOffsetFromOffset(offset int64) int64 {
	return (r.offset + offset) % r.pieceLength
}

func (r *reader) pieceOffset(piece int) int64 {
	return int64(piece)*r.pieceLength - r.offset
}

func (r *reader) Read(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	startPiece := r.pieceFromOffset(r.pos)
	endPiece := r.pieceFromOffset(r.pos + int64(len(b)))
	r.setPiecesPriorities(startPiece, endPiece-startPiece)
	for p := startPiece; p <= endPiece; p++ {
		if !r.torrent.handle.HavePiece(p) {
			if err := r.waitForPiece(p, waitForPieceTimeout); err != nil {
				return 0, err
			}
		}
	}

	storageError := libtorrent.NewStorageError()
	defer libtorrent.DeleteStorageError(storageError)
	n := r.storage.Read(b, int64(len(b)), startPiece, int(r.pieceOffsetFromOffset(r.pos)), storageError)
	if ec := storageError.GetEc(); ec.Failed() {
		message := ec.Message().(string)
		log.Errorf("Storage read error: %s", message)
		return n, errors.New(message)
	}

	r.pos += int64(n)
	return n, nil
}

func (r *reader) Close() error {
	log.Debugf("Closing reader for '%s'", r.torrent.infoHash)
	close(r.closing)
	return nil
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
	r.mu.Lock()
	defer r.mu.Unlock()

	switch whence {
	case io.SeekStart:
		// do nothing
	case io.SeekCurrent:
		off += r.pos
	case io.SeekEnd:
		off = r.length - off
	default:
		off = -1
	}

	if off < 0 || off > r.length {
		return off, InvalidWhenceError
	}

	r.pos = off
	r.setPiecesPriorities(r.pieceFromOffset(off), 0)
	return off, nil
}
