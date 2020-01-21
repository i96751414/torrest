package bittorrent

import (
	"os"
	"path/filepath"

	"github.com/i96751414/libtorrent-go"
)

type File struct {
	torrent      *Torrent
	index        int
	offset       int64
	length       int64
	path         string
	name         string
	pieceLength  int64
	bufferPieces []int
	bufferSize   int64
	isBuffering  bool
}

type FileInfo struct {
	Id     int    `json:"id"`
	Length int64  `json:"length"`
	Path   string `json:"path"`
	Name   string `json:"name"`
}

func NewFile(torrent *Torrent, storage libtorrent.FileStorage, index int) *File {
	return &File{
		torrent:     torrent,
		index:       index,
		offset:      storage.FileOffset(index),
		length:      storage.FileSize(index),
		path:        storage.FilePath(index),
		name:        storage.FileName(index),
		pieceLength: int64(torrent.TorrentInfo().PieceLength()),
	}
}

func (f *File) Info() *FileInfo {
	return &FileInfo{
		Id:     f.index,
		Length: f.length,
		Path:   f.path,
		Name:   f.name,
	}
}
func (f *File) Id() int {
	return f.index
}

func (f *File) Length() int64 {
	return f.length
}

func (f *File) Path() string {
	return f.path
}

func (f *File) Name() string {
	return f.name
}

func (f *File) NewReader() (Reader, error) {
	file, err := os.Open(f.GetDownloadPath())
	if err != nil {
		return nil, err
	}
	// make sure we don't open a file that's locked, as it can happen
	// on BSD systems (darwin included)
	if err := unlockFile(file); err != nil {
		log.Errorf("Unable to unlock file because: %s", err)
	}

	return &reader{
		storage:        file,
		torrent:        f.torrent,
		offset:         f.offset,
		length:         f.length,
		pieceLength:    f.pieceLength,
		priorityPieces: int(float64(f.length/f.pieceLength) * 0.01),
		closing:        make(chan interface{}),
	}, nil
}

func (f *File) GetDownloadPath() string {
	return filepath.Join(f.torrent.service.config.DownloadPath, f.path)
}

func (f *File) getPiecesIndexes(off, length int64) (firstPieceIndex, endPieceIndex int) {
	if off < 0 {
		off = 0
	}
	end := off + length
	if end > f.length {
		end = f.length
	}
	firstPieceIndex = int((f.offset + off) / f.pieceLength)
	endPieceIndex = int((f.offset + end) / f.pieceLength)
	return
}

func (f *File) BytesCompleted() int64 {
	return f.torrent.getFilesDownloadedBytes()[f.index]
}

func (f *File) SetPriority(priority uint) {
	f.torrent.handle.FilePriority(f.index, priority)
}

func (f *File) GetPriority() uint {
	return f.torrent.handle.FilePriority(f.index).(uint)
}

func (f *File) IsDownloading() bool {
	return f.isBuffering || f.GetPriority() > 0
}

func (f *File) Buffer(startBufferSize, endBufferSize int64) {
	f.bufferSize = 0
	f.bufferPieces = nil
	bufferSize := startBufferSize + endBufferSize
	info := f.torrent.TorrentInfo()

	if f.length >= bufferSize {
		aFirstPieceIndex, aEndPieceIndex := f.getPiecesIndexes(0, startBufferSize)
		for idx := aFirstPieceIndex; idx <= aEndPieceIndex; idx++ {
			f.torrent.handle.PiecePriority(idx, TopPriority)
			f.torrent.handle.SetPieceDeadline(idx, 0)
			f.bufferSize += int64(info.PieceSize(idx))
			f.bufferPieces = append(f.bufferPieces, idx)
		}

		bFirstPieceIndex, bEndPieceIndex := f.getPiecesIndexes(f.length-endBufferSize, endBufferSize)
		for idx := bFirstPieceIndex; idx <= bEndPieceIndex; idx++ {
			f.torrent.handle.PiecePriority(idx, TopPriority)
			f.torrent.handle.SetPieceDeadline(idx, 0)
			f.bufferSize += int64(info.PieceSize(idx))
			f.bufferPieces = append(f.bufferPieces, idx)
		}
	} else {
		firstPieceIndex, endPieceIndex := f.getPiecesIndexes(0, f.length)
		for idx := firstPieceIndex; idx <= endPieceIndex; idx++ {
			f.torrent.handle.PiecePriority(idx, TopPriority)
			f.torrent.handle.SetPieceDeadline(idx, 0)
			f.bufferSize += int64(info.PieceSize(idx))
			f.bufferPieces = append(f.bufferPieces, idx)
		}
	}

	f.isBuffering = true
	f.torrent.service.setBufferingRateLimit(false)
}

func (f *File) bufferBytesMissing() int64 {
	return f.torrent.piecesBytesMissing(f.bufferPieces)
}

func (f *File) GetBufferingProgress() float64 {
	if f.bufferSize == 0 {
		return 0
	}
	return float64(f.bufferSize-f.bufferBytesMissing()) / float64(f.bufferSize) * 100.0
}

func (f *File) GetState() LTStatus {
	return f.torrent.getState(f)
}
