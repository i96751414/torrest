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

//noinspection GoUnusedConst
const (
	DontDownloadPriority = uint(0)
	LowPriority          = uint(1)
	DefaultPriority      = uint(4)
	HighPriority         = uint(6)
	TopPriority          = uint(7)
)

type Torrent struct {
	service      *Service
	handle       libtorrent.TorrentHandle
	infoHash     string
	defaultName  string
	mu           *sync.RWMutex
	closing      chan interface{}
	isPaused     bool
	files        []*File
	spaceChecked bool
	hasMetadata  bool
}

type TorrentInfo struct {
	InfoHash string `json:"info_hash"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
}

type TorrentStatus struct {
	Total           int64    `json:"total"`
	TotalDone       int64    `json:"total_done"`
	TotalWanted     int64    `json:"total_wanted"`
	TotalWantedDone int64    `json:"total_wanted_done"`
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
	paused := hasFlagsUint64(flags, libtorrent.GetPaused()) && !hasFlagsUint64(flags, libtorrent.GetAutoManaged())
	status := handle.Status(libtorrent.TorrentHandleQueryName)
	defer libtorrent.DeleteTorrentStatus(status)
	name := status.GetName()
	if len(name) == 0 {
		name = infoHash
	}

	t := &Torrent{
		service:     service,
		handle:      handle,
		infoHash:    infoHash,
		defaultName: name,
		mu:          &sync.RWMutex{},
		closing:     make(chan interface{}),
		isPaused:    paused,
	}

	if status.GetHasMetadata() {
		t.onMetadataReceived()
	}

	return t
}

func (t *Torrent) onMetadataReceived() {
	info := t.handle.TorrentFile()
	files := info.Files()
	fileCount := info.NumFiles()

	f := make([]*File, fileCount)
	for i := 0; i < fileCount; i++ {
		f[i] = NewFile(t, files, i)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.files = f
	t.hasMetadata = true
}

func (t *Torrent) InfoHash() string {
	return t.infoHash
}

func (t *Torrent) Pause() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.handle.UnsetFlags(libtorrent.GetAutoManaged())
	t.handle.Pause(libtorrent.TorrentHandleClearDiskCache)
	t.isPaused = true
}

func (t *Torrent) Resume() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.handle.SetFlags(libtorrent.GetAutoManaged())
	t.isPaused = false
}

func (t *Torrent) getState(file ...*File) LTStatus {
	if t.isPaused {
		return PausedStatus
	}
	if hasFlagsUint64(t.handle.Flags(), libtorrent.GetPaused()|libtorrent.GetAutoManaged()) {
		return QueuedStatus
	}
	if !t.hasMetadata {
		return FindingStatus
	}

	status := t.handle.Status()
	defer libtorrent.DeleteTorrentStatus(status)
	state := LTStatus(status.GetState())

	if state == DownloadingStatus {
		downloading := false
		for _, f := range file {
			if f.isBuffering {
				return BufferingStatus
			}
			if f.priority != DontDownloadPriority {
				downloading = true
			}
		}
		if !downloading || t.getFilesProgress(file...) == 100 {
			return FinishedStatus
		}
	}

	return state
}

func (t *Torrent) GetState() LTStatus {
	return t.getState(t.files...)
}

func (t *Torrent) HasMetadata() bool {
	return t.hasMetadata
}

func (t *Torrent) GetInfo() *TorrentInfo {
	torrentInfo := &TorrentInfo{InfoHash: t.infoHash}
	if info := t.handle.TorrentFile(); info.Swigcptr() != 0 {
		torrentInfo.Name = info.Name()
		torrentInfo.Size = info.TotalSize()
	} else {
		torrentInfo.Name = t.defaultName
	}
	return torrentInfo
}

func (t *Torrent) GetStatus() *TorrentStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	status := t.handle.Status()
	defer libtorrent.DeleteTorrentStatus(status)

	seeders := status.GetNumSeeds()
	seedersTotal := status.GetNumComplete()
	if seedersTotal < 0 {
		seedersTotal = seeders
	}

	peers := status.GetNumPeers() - seeders
	peersTotal := status.GetNumIncomplete()
	if peersTotal < 0 {
		peersTotal = peers
	}

	return &TorrentStatus{
		Total:           status.GetTotal(),
		TotalDone:       status.GetTotalDone(),
		TotalWanted:     status.GetTotalWanted(),
		TotalWantedDone: status.GetTotalWantedDone(),
		Progress:        float64(status.GetProgress()) * 100,
		DownloadRate:    status.GetDownloadRate(),
		UploadRate:      status.GetUploadRate(),
		Paused:          t.isPaused,
		HasMetadata:     t.hasMetadata,
		State:           t.GetState(),
		Seeders:         seeders,
		SeedersTotal:    seedersTotal,
		Peers:           peers,
		PeersTotal:      peersTotal,
		SeedingTime:     status.GetSeedingDuration(),
		FinishedTime:    status.GetFinishedDuration(),
		ActiveTime:      status.GetActiveDuration(),
		AllTimeDownload: status.GetAllTimeDownload(),
		AllTimeUpload:   status.GetAllTimeUpload(),
	}
}

func (t *Torrent) Files() ([]*File, error) {
	if !t.hasMetadata {
		return nil, NoMetadataError
	}
	files := make([]*File, len(t.files))
	copy(files, t.files)
	return files, nil
}

func (t *Torrent) GetFile(id int) (*File, error) {
	if !t.hasMetadata {
		return nil, NoMetadataError
	}
	if id < 0 || id >= len(t.files) {
		return nil, InvalidFileIdError
	}
	return t.files[id], nil
}

func (t *Torrent) SetPriority(priority uint) error {
	log.Debugf("Setting torrent %s with priority %d", t.infoHash, priority)
	if !t.hasMetadata {
		return NoMetadataError
	}
	for _, f := range t.files {
		f.SetPriority(priority)
	}
	return nil
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

func (t *Torrent) piecesBytesMissing(pieces []int) (missing int64) {
	queue := libtorrent.NewStdVectorPartialPieceInfo()
	defer libtorrent.DeleteStdVectorPartialPieceInfo(queue)
	t.handle.GetDownloadQueue(queue)
	info := t.handle.TorrentFile()

	for _, piece := range pieces {
		if !t.handle.HavePiece(piece) {
			missing += int64(info.PieceSize(piece))
		}
	}

	for i := 0; i < int(queue.Size()); i++ {
		ppi := queue.Get(i)
		if containsInt(pieces, ppi.GetPieceIndex()) {
			blocks := ppi.Blocks()
			blocksInPiece := ppi.GetBlocksInPiece()
			for b := 0; b < blocksInPiece; b++ {
				missing -= int64(blocks.Getitem(b).GetBytesProgress())
			}
		}
	}
	return
}

func (t *Torrent) getFilesProgress(file ...*File) float64 {
	var total int64
	var completed int64

	progresses := t.getFilesDownloadedBytes()
	for _, f := range file {
		total += f.length
		completed += progresses[f.index]
	}

	if total == 0 {
		return 100
	}

	progress := float64(completed) / float64(total) * 100.0
	if progress > 100 {
		progress = 100
	}

	return progress
}

func (t *Torrent) checkAvailableSpace() {
	if t.spaceChecked || !t.service.config.CheckAvailableSpace {
		return
	}
	if diskStatus, err := diskusage.DiskUsage(t.service.config.DownloadPath); err != nil {
		log.Warningf("Unable to retrieve the free space for %s", t.service.config.DownloadPath)
		return
	} else if diskStatus != nil {
		status := t.handle.Status(libtorrent.TorrentHandleQueryAccurateDownloadCounters |
			libtorrent.TorrentHandleQuerySavePath | libtorrent.TorrentHandleQueryName)
		defer libtorrent.DeleteTorrentStatus(status)

		if !status.GetHasMetadata() {
			log.Warning("Missing torrent metadata to check available space")
			return
		}

		totalSize := status.GetTotal()
		totalDone := status.GetTotalDone()
		sizeLeft := totalSize - totalDone
		path := status.GetSavePath()

		log.Infof("Checking for sufficient space on %s", path)
		log.Infof("Total size: %s", humanize.Bytes(uint64(totalSize)))
		log.Infof("Total done size: %s", humanize.Bytes(uint64(totalDone)))
		log.Infof("Size left to download: %s", humanize.Bytes(uint64(sizeLeft)))
		log.Infof("Available space: %s", humanize.Bytes(uint64(diskStatus.Free)))

		if diskStatus.Free < sizeLeft {
			log.Errorf("Insufficient free space on %s: has %d, needs %d", path, diskStatus.Free, sizeLeft)
			log.Infof("Pausing torrent %s", status.GetName())
			t.Pause()
		} else {
			t.spaceChecked = true
		}
	}
}

func (t *Torrent) close() {
	close(t.closing)
	libtorrent.DeleteTorrentHandle(t.handle)
}
