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
	infoHash     string
	mu           *sync.RWMutex
	closing      chan interface{}
	isPaused     bool
	files        []*File
	spaceChecked bool
}

type TorrentStatus struct {
	Name            string   `json:"name"`
	Progress        float64  `json:"progress"`
	DownloadRate    int      `json:"download_rate"`
	UploadRate      int      `json:"upload_rate"`
	Paused          bool     `json:"paused"`
	HasMetadata     bool     `json:"has_metadata"`
	State           LTStatus `json:"state"`
	Seeders         int      `json:"seeders"`
	SeedersTotal    int      `json:"seeders_total"`
	Peers           int      `json:"peers"`
	PeersTotal      int      `json:"peers_total"`
	SeedingTime     int64    `json:"seeding_time"`
	FinishedTime    int64    `json:"finished_time"`
	ActiveTime      int64    `json:"active_time"`
	AllTimeDownload int64    `json:"all_time_download"`
	AllTimeUpload   int64    `json:"all_time_upload"`
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

func NewTorrent(service *Service, handle libtorrent.TorrentHandle, infoHash string) *Torrent {
	flags := handle.Flags()
	paused := hasFlags(flags, libtorrent.GetPaused()) && !hasFlags(flags, libtorrent.GetAutoManaged())

	return &Torrent{
		service:  service,
		handle:   handle,
		infoHash: infoHash,
		mu:       &sync.RWMutex{},
		closing:  make(chan interface{}),
		isPaused: paused,
	}
}

func (t *Torrent) InfoHash() string {
	return t.infoHash
}

func (t *Torrent) Pause() {
	t.handle.UnsetFlags(libtorrent.GetAutoManaged())
	t.handle.Pause(1)
	t.isPaused = true
}

func (t *Torrent) Resume() {
	t.handle.SetFlags(libtorrent.GetAutoManaged())
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

func (t *Torrent) HasMetadata() bool {
	return t.handle.Status().GetHasMetadata()
}

func (t *Torrent) GetStatus() *TorrentStatus {
	status := t.handle.Status(libtorrent.TorrentHandleQueryName)
	seeders := status.GetNumSeeds()

	return &TorrentStatus{
		Name:            status.GetName(),
		Progress:        float64(status.GetProgress()) * 100,
		DownloadRate:    status.GetDownloadRate(),
		UploadRate:      status.GetUploadRate(),
		Paused:          t.isPaused,
		HasMetadata:     status.GetHasMetadata(),
		State:           t.GetState(),
		Seeders:         seeders,
		SeedersTotal:    status.GetNumComplete(),
		Peers:           status.GetNumPeers() - seeders,
		PeersTotal:      status.GetNumIncomplete(),
		SeedingTime:     status.GetSeedingDuration(),
		FinishedTime:    status.GetFinishedDuration(),
		ActiveTime:      status.GetActiveDuration(),
		AllTimeDownload: status.GetAllTimeDownload(),
		AllTimeUpload:   status.GetAllTimeUpload(),
	}
}

func (t *Torrent) TorrentInfo() libtorrent.TorrentInfo {
	return t.handle.TorrentFile()
}

func (t *Torrent) Files() []*File {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.files == nil {
		if info := t.TorrentInfo(); info.Swigcptr() != 0 {
			files := info.Files()
			t.files = make([]*File, info.NumFiles())
			for i := 0; i < info.NumFiles(); i++ {
				t.files[i] = NewFile(t, files, i)
			}
		}
	}
	return t.files
}

func (t *Torrent) GetFile(id int) (*File, error) {
	files := t.Files()
	if id < 0 || id >= len(files) {
		return nil, InvalidFileIdError
	}
	return files[id], nil
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

		status := t.handle.Status(libtorrent.TorrentHandleQueryAccurateDownloadCounters |
			libtorrent.TorrentHandleQuerySavePath)
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
			log.Infof("Pausing torrent %s", t.handle.Status(libtorrent.TorrentHandleQueryName).GetName())
			t.Pause()
		} else {
			t.spaceChecked = true
		}
	}
}

func hasFlags(flags, f uint64) bool {
	return flags&f == f
}
