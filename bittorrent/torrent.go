package bittorrent

import (
	"bytes"
	"sync"

	"github.com/dustin/go-humanize"
	"github.com/i96751414/libtorrent-go"
	"github.com/i96751414/torrest/diskusage"
	"github.com/zeebo/bencode"
)

type LTStatus int

//noinspection GoUnusedConst
const (
	QueuedStatus             LTStatus = iota // libtorrent.TorrentStatusUnusedEnumForBackwardsCompatibility
	CheckingStatus                           // libtorrent.TorrentStatusCheckingFiles
	FindingStatus                            // libtorrent.TorrentStatusDownloadingMetadata
	DownloadingStatus                        // libtorrent.TorrentStatusDownloading
	FinishedStatus                           // libtorrent.TorrentStatusFinished
	SeedingStatus                            // libtorrent.TorrentStatusSeeding
	AllocatingStatus                         // libtorrent.TorrentStatusAllocating
	CheckingResumeDataStatus                 // libtorrent.TorrentStatusCheckingResumeData
	// Custom status
	PausedStatus
	BufferingStatus
)

type Torrent struct {
	service      *Service
	handle       libtorrent.TorrentHandle
	mu           *sync.RWMutex
	closing      chan interface{}
	isPaused     bool
	files        []*File
	spaceChecked bool
}

type TorrentFileRaw struct {
	Announce     string                 `bencode:"announce"`
	AnnounceList [][]string             `bencode:"announce-list"`
	Info         map[string]interface{} `bencode:"info"`
}

func DecodeTorrentData(data []byte) (*TorrentFileRaw, error) {
	var torrentFile *TorrentFileRaw
	dec := bencode.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&torrentFile); err != nil {
		return nil, err
	}
	return torrentFile, nil
}

func NewTorrent(service *Service, handle libtorrent.TorrentHandle) *Torrent {
	status := handle.Status()
	paused := status.GetPaused() && !status.GetAutoManaged()

	return &Torrent{
		service:  service,
		handle:   handle,
		mu:       &sync.RWMutex{},
		closing:  make(chan interface{}),
		isPaused: paused,
	}
}

func (t *Torrent) Pause() {
	t.handle.AutoManaged(false)
	t.handle.Pause(1)
	t.isPaused = true
}

func (t *Torrent) Resume() {
	t.handle.AutoManaged(true)
	t.isPaused = false
}

func (t *Torrent) getState(file ...*File) LTStatus {
	if t.isPaused {
		return PausedStatus
	}
	state := LTStatus(t.handle.Status().GetState())
	if state == DownloadingStatus {
		for _, f := range file {
			if f.isBuffering {
				return BufferingStatus
			}
		}
	}
	return state
}

func (t *Torrent) GetState() LTStatus {
	return t.getState(t.Files()...)
}

func (t *Torrent) TorrentInfo() libtorrent.TorrentInfo {
	return t.handle.TorrentFile()
}

func (t *Torrent) Files() []*File {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.files == nil {
		if info := t.TorrentInfo(); info != nil {
			files := info.Files()
			t.files = make([]*File, info.NumFiles())
			for i := 0; i < info.NumFiles(); i++ {
				t.files[i] = NewFile(t, files, i)
			}
		}
	}
	return t.files
}

func (t *Torrent) getFilesDownloadedBytes() []int64 {
	pVec := libtorrent.NewStdVectorSizeType()
	defer libtorrent.DeleteStdVectorSizeType(pVec)

	t.handle.FileProgress(pVec, int(libtorrent.TorrentHandlePieceGranularity))
	progresses := make([]int64, pVec.Size())
	for i := 0; i < int(pVec.Size()); i++ {
		progresses[i] = pVec.Get(i)
	}
	return progresses
}

func containsInt(arr []int, value int) bool {
	for _, a := range arr {
		if a == value {
			return true
		}
	}
	return false
}

func (t *Torrent) piecesBytesMissing(pieces []int) (missing int64) {
	queue := libtorrent.NewStdVectorPartialPieceInfo()
	defer libtorrent.DeleteStdVectorPartialPieceInfo(queue)
	t.handle.GetDownloadQueue(queue)

	for _, piece := range pieces {
		if !t.handle.HavePiece(piece) {
			missing += int64(t.TorrentInfo().PieceSize(piece))
		}
	}

	for i := 0; i < int(queue.Size()); i++ {
		ppi := queue.Get(i)
		if containsInt(pieces, ppi.GetPieceIndex()) {
			blocks := ppi.Blocks()
			for b := 0; b < ppi.GetBlocksInPiece(); b++ {
				missing -= int64(blocks.Getitem(b).GetBytesProgress())
			}
		}
	}
	return
}

func (t *Torrent) checkAvailableSpace() {
	if t.spaceChecked {
		return
	}
	if diskStatus, err := diskusage.DiskUsage(t.service.config.DownloadPath); err != nil {
		log.Warningf("Unable to retrieve the free space for %s, continuing anyway...", t.service.config.DownloadPath)
		return
	} else if diskStatus != nil {
		torrentInfo := t.TorrentInfo()
		if torrentInfo == nil || torrentInfo.Swigcptr() == 0 {
			log.Warning("Missing torrent info to check available space.")
			return
		}

		status := t.handle.Status(uint(libtorrent.TorrentHandleQueryAccurateDownloadCounters) |
			uint(libtorrent.TorrentHandleQuerySavePath))
		totalSize := torrentInfo.TotalSize()
		totalDone := status.GetTotalDone()
		sizeLeft := totalSize - totalDone
		availableSpace := diskStatus.Free
		path := status.GetSavePath()

		log.Infof("Checking for sufficient space on %s...", path)
		log.Infof("Total size of download: %s", humanize.Bytes(uint64(totalSize)))
		log.Infof("All time download: %s", humanize.Bytes(uint64(status.GetAllTimeDownload())))
		log.Infof("Size total done: %s", humanize.Bytes(uint64(totalDone)))
		log.Infof("Size left to download: %s", humanize.Bytes(uint64(sizeLeft)))
		log.Infof("Available space: %s", humanize.Bytes(uint64(availableSpace)))

		if availableSpace < sizeLeft {
			log.Errorf("Unsufficient free space on %s. Has %d, needs %d.", path, diskStatus.Free, sizeLeft)
			log.Infof("Pausing torrent %s", t.handle.Status(uint(libtorrent.TorrentHandleQueryName)).GetName())
			t.Pause()
		} else {
			t.spaceChecked = true
		}
	}
}
