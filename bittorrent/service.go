package bittorrent

import (
	"encoding/hex"
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/i96751414/libtorrent-go"
	"github.com/i96751414/torrest/settings"
	"github.com/i96751414/torrest/util"
	"github.com/op/go-logging"
)

var (
	log       = logging.MustGetLogger("bittorrent")
	alertsLog = logging.MustGetLogger("alerts")
	portRegex = regexp.MustCompile(`:\d+$`)
)

var DefaultDhtBootstrapNodes = []string{
	"router.utorrent.com:6881",
	"router.bittorrent.com:6881",
	"dht.transmissionbt.com:6881",
	"dht.aelitis.com:6881",     // Vuze
	"router.silotis.us:6881",   // IPv6
	"dht.libtorrent.org:25401", // @arvidn's
}

const (
	libtorrentAlertWaitTime = time.Second
	libtorrentProgressTime  = time.Second
	maxFilesPerTorrent      = 1000
)

//noinspection GoUnusedConst
const (
	ipToSDefault     = iota
	ipToSLowDelay    = 1 << iota
	ipToSReliability = 1 << iota
	ipToSThroughput  = 1 << iota
	ipToSLowCost     = 1 << iota
)

const (
	extTorrent    = ".torrent"
	extMagnet     = ".magnet"
	extParts      = ".parts"
	extFastResume = ".fastresume"
)

// Service represents the torrent service
type Service struct {
	session      libtorrent.Session
	config       *settings.Settings
	settingsPack libtorrent.SettingsPack
	torrents     []*Torrent
	mu           *sync.RWMutex
	wg           *sync.WaitGroup
	closing      chan interface{}
	UserAgent    string
	downloadRate int64
	uploadRate   int64
	progress     float64
}

type ServiceStatus struct {
	Progress     float64 `json:"progress"`
	DownloadRate int64   `json:"download_rate"`
	UploadRate   int64   `json:"upload_rate"`
	NumTorrents  int     `json:"num_torrents"`
	IsPaused     bool    `json:"is_paused"`
}

type Magnet struct {
	Uri      string
	Download bool
}

// NewService creates a service given the provided configs
func NewService(config *settings.Settings) *Service {
	createDir(config.DownloadPath)
	createDir(config.TorrentsPath)

	s := &Service{
		settingsPack: libtorrent.NewSettingsPack(),
		mu:           &sync.RWMutex{},
		wg:           &sync.WaitGroup{},
		closing:      make(chan interface{}),
	}

	s.configure(config)
	s.loadTorrentFiles()

	s.wg.Add(3)
	go s.saveResumeDataLoop()
	go s.alertsConsumer()
	go s.downloadProgress()

	return s
}

func (s *Service) alertsConsumer() {
	defer s.wg.Done()
	ipRegex := regexp.MustCompile(`\.\d+`)
	for {
		select {
		case <-s.closing:
			return
		default:
			if s.session.WaitForAlert(libtorrentAlertWaitTime).Swigcptr() == 0 {
				continue
			}

			alerts := libtorrent.NewStdVectorAlerts()
			s.session.PopAlerts(alerts)

			for i := 0; i < int(alerts.Size()); i++ {
				ltAlert := alerts.Get(i)
				alertType := ltAlert.Type()
				alertPtr := ltAlert.Swigcptr()
				alertMessage := ltAlert.Message()
				category := ltAlert.Category()
				what := ltAlert.What()

				switch alertType {
				case libtorrent.SaveResumeDataAlertAlertType:
					s.onSaveResumeData(libtorrent.SwigcptrSaveResumeDataAlert(alertPtr))

				case libtorrent.ExternalIpAlertAlertType:
					alertMessage = ipRegex.ReplaceAllString(alertMessage, ".XX")

				case libtorrent.MetadataReceivedAlertAlertType:
					s.onMetadataReceived(libtorrent.SwigcptrMetadataReceivedAlert(alertPtr))

				case libtorrent.StateChangedAlertAlertType:
					s.onStateChanged(libtorrent.SwigcptrStateChangedAlert(alertPtr))
				}

				// log alerts
				var logFunc func(string, ...interface{})
				if category&libtorrent.AlertErrorNotification != 0 {
					logFunc = alertsLog.Errorf
				} else if category&libtorrent.AlertConnectNotification != 0 {
					logFunc = alertsLog.Debugf
				} else if category&libtorrent.AlertPerformanceWarning != 0 {
					logFunc = alertsLog.Warningf
				} else {
					logFunc = alertsLog.Noticef
				}
				logFunc("%s: %s", what, alertMessage)
			}
			libtorrent.DeleteStdVectorAlerts(alerts)
		}
	}
}

func (s *Service) onSaveResumeData(alert libtorrent.SaveResumeDataAlert) {
	torrentStatus := alert.GetHandle().Status(libtorrent.TorrentHandleQueryName)
	defer libtorrent.DeleteTorrentStatus(torrentStatus)
	infoHash := getInfoHash(torrentStatus.GetInfoHash())

	params := alert.GetParams()
	entry := libtorrent.WriteResumeData(params)
	defer libtorrent.DeleteEntry(entry)

	bEncoded := []byte(libtorrent.Bencode(entry))
	if _, e1 := DecodeTorrentData(bEncoded); e1 == nil {
		if e2 := ioutil.WriteFile(s.fastResumeFilePath(infoHash), bEncoded, 0644); e2 != nil {
			log.Errorf("Failed saving '%s.fastresume': %s", infoHash, e2)
		}
	} else {
		log.Warningf("Resume data corrupted for %s, %d bytes received and failed to decode with: %s",
			torrentStatus.GetName(), len(bEncoded), e1)
	}
}

func (s *Service) onMetadataReceived(alert libtorrent.MetadataReceivedAlert) {
	torrentInfo := alert.GetHandle().TorrentFile()
	infoHash := getInfoHash(torrentInfo.InfoHash())

	if torrent, err := s.GetTorrent(infoHash); err == nil {
		torrent.onMetadataReceived()
	} else {
		log.Errorf("Unable to get torrent with infohash %s. Skipping onMetadataReceived", infoHash)
	}

	log.Debugf("Saving %s.torrent", infoHash)
	torrentFile := libtorrent.NewCreateTorrent(torrentInfo)
	defer libtorrent.DeleteCreateTorrent(torrentFile)
	torrentContent := torrentFile.Generate()
	defer libtorrent.DeleteEntry(torrentContent)

	bEncodedTorrent := []byte(libtorrent.Bencode(torrentContent))
	if err := ioutil.WriteFile(s.torrentPath(infoHash), bEncodedTorrent, 0644); err == nil {
		s.deleteMagnetFile(infoHash)
	} else {
		log.Errorf("Failed saving '%s.torrent': %s", infoHash, err)
	}
}

func (s *Service) onStateChanged(alert libtorrent.StateChangedAlert) {
	switch alert.GetState() {
	case libtorrent.TorrentStatusDownloading:
		infoHash := getHandleInfoHash(alert.GetHandle())
		if torrent, err := s.GetTorrent(infoHash); err == nil {
			torrent.checkAvailableSpace()
		}
	}
}

func getHandleInfoHash(handle libtorrent.TorrentHandle) string {
	sha1Hash := handle.InfoHash()
	defer libtorrent.DeleteSha1_hash(sha1Hash)
	return getInfoHash(sha1Hash)
}

func getInfoHash(hash libtorrent.Sha1_hash) string {
	return hex.EncodeToString([]byte(hash.ToString()))
}

func (s *Service) saveResumeDataLoop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.closing:
			return
		case <-time.After(s.config.SessionSave * time.Second):
			s.mu.RLock()
			for _, torrent := range s.torrents {
				if torrent.handle.IsValid() {
					status := torrent.handle.Status()
					if status.GetHasMetadata() && status.GetNeedSaveResume() {
						torrent.handle.SaveResumeData(libtorrent.TorrentHandleSaveInfoDict)
					}
					libtorrent.DeleteTorrentStatus(status)
				}
			}
			s.mu.RUnlock()
		}
	}
}

func (s *Service) stopServices() {
	log.Debug("Stopping LSD/DHT/UPNP/NAT-PPM")
	s.settingsPack.SetBool("enable_lsd", false)
	s.settingsPack.SetBool("enable_dht", false)
	s.settingsPack.SetBool("enable_upnp", false)
	s.settingsPack.SetBool("enable_natpmp", false)
	s.session.ApplySettings(s.settingsPack)
}

func (s *Service) removeTorrents() {
	for _, torrent := range s.torrents {
		torrent.remove(false)
	}
	s.torrents = nil
}

func (s *Service) Close() {
	log.Info("Stopping Service")
	s.stopServices()

	log.Debug("Closing service routines")
	close(s.closing)
	s.wg.Wait()

	log.Debug("Destroying service")
	s.removeTorrents()
	libtorrent.DeleteSession(s.session)
	libtorrent.DeleteSettingsPack(s.settingsPack)
}

func (s *Service) Reconfigure(config *settings.Settings, reset bool) {
	log.Info("Reconfiguring Service")
	s.mu.Lock()
	defer s.mu.Unlock()

	createDir(config.DownloadPath)
	createDir(config.TorrentsPath)

	s.configure(config)

	if reset {
		log.Debug("Resetting torrents")
		s.removeTorrents()
		s.loadTorrentFiles()
	}
}

func (s *Service) configure(config *settings.Settings) {
	s.config = config

	logging.SetLevel(s.config.ServiceLogLevel, log.Module)
	logging.SetLevel(s.config.AlertsLogLevel, alertsLog.Module)

	log.Info("Applying session settings")

	s.UserAgent = util.UserAgent()
	if s.config.UserAgent > 0 {
		switch s.config.UserAgent {
		case settings.LibtorrentUA:
			s.UserAgent = "libtorrent/" + libtorrent.Version()
		case settings.LibtorrentRasterbar_1_1_0_UA:
			s.UserAgent = "libtorrent (Rasterbar) 1.1.0"
		case settings.BitTorrent_7_5_0_UA:
			s.UserAgent = "BitTorrent 7.5.0"
		case settings.BitTorrent_7_4_3_UA:
			s.UserAgent = "BitTorrent 7.4.3"
		case settings.UTorrent_3_4_9_UA:
			s.UserAgent = "µTorrent 3.4.9"
		case settings.UTorrent_3_2_0_UA:
			s.UserAgent = "µTorrent 3.2.0"
		case settings.UTorrent_2_2_1_UA:
			s.UserAgent = "µTorrent 2.2.1"
		case settings.Transmission_2_92_UA:
			s.UserAgent = "Transmission 2.92"
		case settings.Deluge_1_3_6_0_UA:
			s.UserAgent = "Deluge 1.3.6.0"
		case settings.Deluge_1_3_12_0_UA:
			s.UserAgent = "Deluge 1.3.12.0"
		case settings.Vuze_5_7_3_0_UA:
			s.UserAgent = "Vuze 5.7.3.0"
		default:
			log.Warning("Invalid user agent provided: using default")
		}
	}
	log.Infof("UserAgent: %s", s.UserAgent)

	if s.config.UserAgent != settings.LibtorrentUA {
		s.settingsPack.SetStr("user_agent", s.UserAgent)
	}
	s.settingsPack.SetInt("request_timeout", 2)
	s.settingsPack.SetInt("peer_connect_timeout", 2)
	s.settingsPack.SetBool("strict_end_game_mode", true)
	s.settingsPack.SetBool("announce_to_all_trackers", true)
	s.settingsPack.SetBool("announce_to_all_tiers", true)
	s.settingsPack.SetInt("connection_speed", 500)
	s.settingsPack.SetInt("download_rate_limit", 0)
	s.settingsPack.SetInt("upload_rate_limit", 0)
	s.settingsPack.SetInt("choking_algorithm", 0)
	s.settingsPack.SetInt("share_ratio_limit", 0)
	s.settingsPack.SetInt("seed_time_ratio_limit", 0)
	s.settingsPack.SetInt("seed_time_limit", 0)
	s.settingsPack.SetInt("peer_tos", ipToSLowCost)
	s.settingsPack.SetInt("torrent_connect_boost", 0)
	s.settingsPack.SetBool("rate_limit_ip_overhead", true)
	s.settingsPack.SetBool("no_atime_storage", true)
	s.settingsPack.SetBool("announce_double_nat", true)
	s.settingsPack.SetBool("prioritize_partial_pieces", false)
	s.settingsPack.SetBool("free_torrent_hashes", true)
	s.settingsPack.SetBool("use_parole_mode", true)
	s.settingsPack.SetInt("seed_choking_algorithm", int(libtorrent.SettingsPackFastestUpload))
	s.settingsPack.SetBool("upnp_ignore_nonrouters", true)
	s.settingsPack.SetBool("lazy_bitfields", true)
	s.settingsPack.SetInt("stop_tracker_timeout", 1)
	s.settingsPack.SetInt("auto_scrape_interval", 1200)
	s.settingsPack.SetInt("auto_scrape_min_interval", 900)
	s.settingsPack.SetBool("ignore_limits_on_local_network", true)
	s.settingsPack.SetBool("rate_limit_utp", true)
	s.settingsPack.SetInt("mixed_mode_algorithm", int(libtorrent.SettingsPackPreferTcp))

	// For Android external storage / OS-mounted NAS setups
	if s.config.TunedStorage {
		s.settingsPack.SetBool("use_read_cache", true)
		s.settingsPack.SetBool("coalesce_reads", true)
		s.settingsPack.SetBool("coalesce_writes", true)
		s.settingsPack.SetInt("max_queued_disk_bytes", 10*1024*1024)
		s.settingsPack.SetInt("cache_size", -1)
	}

	if s.config.ConnectionsLimit > 0 {
		s.settingsPack.SetInt("connections_limit", s.config.ConnectionsLimit)
	} else {
		setPlatformSpecificSettings(s.settingsPack)
	}

	if !s.config.LimitAfterBuffering {
		s.settingsPack.SetInt("download_rate_limit", s.config.MaxDownloadRate)
		s.settingsPack.SetInt("upload_rate_limit", s.config.MaxUploadRate)
	}

	if s.config.ShareRatioLimit > 0 {
		s.settingsPack.SetInt("share_ratio_limit", s.config.ShareRatioLimit)
	}
	if s.config.SeedTimeRatioLimit > 0 {
		s.settingsPack.SetInt("seed_time_ratio_limit", s.config.SeedTimeRatioLimit)
	}
	if s.config.SeedTimeLimit > 0 {
		s.settingsPack.SetInt("seed_time_limit", s.config.SeedTimeLimit)
	}

	s.settingsPack.SetInt("active_downloads", s.config.ActiveDownloadsLimit)
	s.settingsPack.SetInt("active_seeds", s.config.ActiveSeedsLimit)
	s.settingsPack.SetInt("active_checking", s.config.ActiveCheckingLimit)
	s.settingsPack.SetInt("active_dht_limit", s.config.ActiveDhtLimit)
	s.settingsPack.SetInt("active_tracker_limit", s.config.ActiveTrackerLimit)
	s.settingsPack.SetInt("active_lsd_limit", s.config.ActiveLsdLimit)
	s.settingsPack.SetInt("active_limit", s.config.ActiveLimit)

	if s.config.EncryptionPolicy == settings.EncryptionDisabledPolicy ||
		s.config.EncryptionPolicy == settings.EncryptionForcedPolicy {
		log.Debug("Applying encryption settings")
		var policy int
		var level int
		var preferRc4 bool

		switch s.config.EncryptionPolicy {
		case settings.EncryptionDisabledPolicy:
			policy = int(libtorrent.SettingsPackPeDisabled)
			level = int(libtorrent.SettingsPackPeBoth)
			preferRc4 = false
		case settings.EncryptionForcedPolicy:
			policy = int(libtorrent.SettingsPackPeForced)
			level = int(libtorrent.SettingsPackPeRc4)
			preferRc4 = true
		}

		s.settingsPack.SetInt("out_enc_policy", policy)
		s.settingsPack.SetInt("in_enc_policy", policy)
		s.settingsPack.SetInt("allowed_enc_level", level)
		s.settingsPack.SetBool("prefer_rc4", preferRc4)
	} else if s.config.EncryptionPolicy != settings.EncryptionEnabledPolicy {
		log.Warning("Invalid encryption policy provided. Using default")
	}

	if s.config.Proxy != nil && s.config.Proxy.Type != settings.ProxyTypeNone {
		log.Debug("Applying proxy settings")
		s.settingsPack.SetInt("proxy_type", int(s.config.Proxy.Type))
		s.settingsPack.SetInt("proxy_port", s.config.Proxy.Port)
		s.settingsPack.SetStr("proxy_hostname", s.config.Proxy.Hostname)
		s.settingsPack.SetStr("proxy_username", s.config.Proxy.Username)
		s.settingsPack.SetStr("proxy_password", s.config.Proxy.Password)
		s.settingsPack.SetBool("proxy_tracker_connections", true)
		s.settingsPack.SetBool("proxy_peer_connections", true)
		s.settingsPack.SetBool("proxy_hostnames", true)
		s.settingsPack.SetBool("force_proxy", true)
		if s.config.Proxy.Type == settings.ProxyTypeI2PSAM {
			s.settingsPack.SetInt("i2p_port", s.config.Proxy.Port)
			s.settingsPack.SetStr("i2p_hostname", s.config.Proxy.Hostname)
			s.settingsPack.SetBool("allows_i2p_mixed", false)
			s.settingsPack.SetBool("allows_i2p_mixed", true)
		}
	}

	// Set alert_mask here so it also applies on reconfigure...
	s.settingsPack.SetInt("alert_mask", int(
		libtorrent.AlertStatusNotification|
			libtorrent.AlertStorageNotification|
			libtorrent.AlertErrorNotification))

	// Start services
	var listenInterfaces []string
	if interfaces := strings.Replace(s.config.ListenInterfaces, " ", "", -1); interfaces != "" {
		listenInterfaces = strings.Split(interfaces, ",")
	} else {
		listenInterfaces = []string{"0.0.0.0", "[::]"}
	}

	listenPort := strconv.FormatUint(uint64(s.config.ListenPort), 10)
	for i, listenInterface := range listenInterfaces {
		if !portRegex.MatchString(listenInterface) {
			listenInterfaces[i] += ":" + listenPort
		}
	}
	s.settingsPack.SetStr("listen_interfaces", strings.Join(listenInterfaces, ","))

	if outInterfaces := strings.Replace(s.config.OutgoingInterfaces, " ", "", -1); outInterfaces != "" {
		s.settingsPack.SetStr("outgoing_interfaces", outInterfaces)
	}

	s.settingsPack.SetStr("dht_bootstrap_nodes", strings.Join(DefaultDhtBootstrapNodes, ","))
	s.settingsPack.SetBool("enable_dht", !s.config.DisableDHT)
	s.settingsPack.SetBool("enable_upnp", !s.config.DisableUPNP)
	s.settingsPack.SetBool("enable_natpmp", !s.config.DisableNatPMP)
	s.settingsPack.SetBool("enable_lsd", !s.config.DisableLSD)

	if s.session == nil {
		log.Debug("First configuration, starting a new session")
		s.session = libtorrent.NewSession(s.settingsPack, libtorrent.SessionHandleAddDefaultPlugins)
	} else {
		log.Debug("Modifying session settings")
		s.session.ApplySettings(s.settingsPack)
	}
}

func (s *Service) setBufferingRateLimit(enable bool) {
	if s.config.LimitAfterBuffering {
		if enable {
			s.settingsPack.SetInt("download_rate_limit", s.config.MaxDownloadRate)
			s.settingsPack.SetInt("upload_rate_limit", s.config.MaxUploadRate)
		} else {
			log.Debug("Resetting rate limiting")
			s.settingsPack.SetInt("download_rate_limit", 0)
			s.settingsPack.SetInt("upload_rate_limit", 0)
		}
		s.session.ApplySettings(s.settingsPack)
	}
}

func (s *Service) addTorrentWithParams(torrentParams libtorrent.AddTorrentParams, infoHash string, isResumeData, noDownload bool) error {
	log.Debugf("Adding torrent params with infohash %s", infoHash)

	if !isResumeData {
		log.Debugf("Setting params for '%s' torrent", infoHash)
		torrentParams.SetSavePath(s.config.DownloadPath)
		// torrentParams.SetStorageMode(libtorrent.StorageModeAllocate)
		torrentParams.SetFlags(torrentParams.GetFlags() | libtorrent.GetSequentialDownload())
	}

	if noDownload {
		log.Debugf("Disabling download for '%s' torrent", infoHash)
		filesPriorities := libtorrent.NewStdVectorChar()
		defer libtorrent.DeleteStdVectorChar(filesPriorities)
		for i := maxFilesPerTorrent; i > 0; i-- {
			filesPriorities.Add(0)
		}
		torrentParams.SetFilePriorities(filesPriorities)
	}

	if _, _, e := s.getTorrent(infoHash); e == nil {
		return DuplicateTorrentError
	} else {
		errorCode := libtorrent.NewErrorCode()
		defer libtorrent.DeleteErrorCode(errorCode)
		torrentHandle := s.session.AddTorrent(torrentParams, errorCode)
		if errorCode.Failed() || !torrentHandle.IsValid() {
			if torrentHandle.Swigcptr() != 0 {
				libtorrent.DeleteTorrentHandle(torrentHandle)
			}
			log.Errorf("Error adding torrent '%s': %v", infoHash, errorCode.Message())
			return LoadTorrentError
		} else {
			s.torrents = append(s.torrents, NewTorrent(s, torrentHandle, infoHash))
		}
	}
	return nil
}

func (s *Service) AddMagnet(magnet string, download bool) (infoHash string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addMagnet(magnet, download, true)
}

func (s *Service) addMagnet(magnet string, download, saveMagnet bool) (infoHash string, err error) {
	log.Debugf("Adding magnet '%s' with download=%t and save=%t", magnet, download, saveMagnet)
	torrentParams := libtorrent.NewAddTorrentParams()
	defer libtorrent.DeleteAddTorrentParams(torrentParams)
	errorCode := libtorrent.NewErrorCode()
	defer libtorrent.DeleteErrorCode(errorCode)

	libtorrent.ParseMagnetUri(magnet, torrentParams, errorCode)
	if errorCode.Failed() {
		return "", errors.New(errorCode.Message().(string))
	}

	infoHash = getInfoHash(torrentParams.GetInfoHash())
	err = s.addTorrentWithParams(torrentParams, infoHash, false, !download)
	if err == nil && saveMagnet {
		if e := saveGobData(s.magnetFilePath(infoHash), Magnet{magnet, download}, 0644); e != nil {
			log.Errorf("Failed saving magnet: %s", e)
		}
	}
	return
}

func (s *Service) AddTorrentData(data []byte, download bool) (infoHash string, err error) {
	log.Debugf("Adding torrent data with download=%t", download)
	errorCode := libtorrent.NewErrorCode()
	defer libtorrent.DeleteErrorCode(errorCode)
	info := libtorrent.NewTorrentInfo(string(data), len(data), errorCode)
	defer libtorrent.DeleteTorrentInfo(info)

	if errorCode.Failed() {
		return "", errors.New(errorCode.Message().(string))
	}

	torrentParams := libtorrent.NewAddTorrentParams()
	defer libtorrent.DeleteAddTorrentParams(torrentParams)
	torrentParams.SetTorrentInfo(info)
	infoHash = getInfoHash(info.InfoHash())

	s.mu.Lock()
	defer s.mu.Unlock()
	err = s.addTorrentWithParams(torrentParams, infoHash, false, !download)
	if err == nil {
		if e := ioutil.WriteFile(s.torrentPath(infoHash), data, 0644); e != nil {
			log.Errorf("Failed saving torrent: %s", e)
		}
	}
	return
}

func (s *Service) AddTorrentFile(torrentFile string, download bool) (infoHash string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addTorrentFile(torrentFile, download)
}

func (s *Service) addTorrentFile(torrentFile string, download bool) (infoHash string, err error) {
	log.Debugf("Adding torrent file '%s' with download=%t", torrentFile, download)
	fi, e := os.Stat(torrentFile)
	if e != nil {
		return "", e
	}

	errorCode := libtorrent.NewErrorCode()
	defer libtorrent.DeleteErrorCode(errorCode)
	info := libtorrent.NewTorrentInfo(torrentFile, errorCode)
	defer libtorrent.DeleteTorrentInfo(info)

	if errorCode.Failed() {
		return "", errors.New(errorCode.Message().(string))
	}

	torrentParams := libtorrent.NewAddTorrentParams()
	defer libtorrent.DeleteAddTorrentParams(torrentParams)
	torrentParams.SetTorrentInfo(info)
	infoHash = getInfoHash(info.InfoHash())

	err = s.addTorrentWithParams(torrentParams, infoHash, false, !download)
	if err == nil {
		torrentDst := s.torrentPath(infoHash)
		if fi2, e1 := os.Stat(torrentDst); e1 != nil || !os.SameFile(fi, fi2) {
			log.Debugf("Copying torrent %s", infoHash)
			if e2 := copyFileContents(torrentFile, torrentDst); e2 != nil {
				log.Errorf("Failed copying torrent: %s", e2)
			}
		}
	}
	return
}

func (s *Service) addTorrentWithResumeData(fastResumeFile string) (err error) {
	log.Debugf("Adding torrent with resume data '%s'", fastResumeFile)
	if fastResumeData, e := ioutil.ReadFile(fastResumeFile); e != nil {
		deleteFile(fastResumeFile)
		err = e
	} else {
		node := libtorrent.NewBdecodeNode()
		defer libtorrent.DeleteBdecodeNode(node)
		errorCode := libtorrent.NewErrorCode()
		defer libtorrent.DeleteErrorCode(errorCode)
		libtorrent.Bdecode(fastResumeData, int64(len(fastResumeData)), node, errorCode)
		if errorCode.Failed() {
			err = errors.New(errorCode.Message().(string))
		} else {
			torrentParams := libtorrent.ReadResumeData(node, errorCode)
			defer libtorrent.DeleteAddTorrentParams(torrentParams)
			if errorCode.Failed() {
				err = errors.New(errorCode.Message().(string))
			} else {
				infoHash := getInfoHash(torrentParams.GetInfoHash())
				err = s.addTorrentWithParams(torrentParams, infoHash, true, false)
			}
		}
	}
	return
}

func (s *Service) loadTorrentFiles() {
	resumeFiles, _ := filepath.Glob(s.fastResumeFilePath("*"))
	for _, fastResumeFile := range resumeFiles {
		if err := s.addTorrentWithResumeData(fastResumeFile); err != nil {
			log.Errorf("Failed adding torrent with resume data: %s", err)
		}
	}

	files, _ := filepath.Glob(s.torrentPath("*"))
	for _, torrentFile := range files {
		if infoHash, err := s.addTorrentFile(torrentFile, false); err == LoadTorrentError {
			s.deletePartsFile(infoHash)
			s.deleteFastResumeFile(infoHash)
			s.deleteTorrentFile(infoHash)
		}
	}

	magnets, _ := filepath.Glob(s.magnetFilePath("*"))
	for _, magnet := range magnets {
		data := Magnet{}
		if err := readGobData(magnet, &data); err == nil {
			if infoHash, e1 := s.addMagnet(data.Uri, data.Download, false); e1 == DuplicateTorrentError {
				if _, t, e2 := s.getTorrent(infoHash); e2 == nil && t.hasMetadata {
					deleteFile(magnet)
				}
			}
		} else {
			log.Errorf("Failed to read magnet file '%s': %s", magnet, err)
		}
	}

	partsFiles, _ := filepath.Glob(s.partsFilePath("*"))
	for _, partsFile := range partsFiles {
		infoHash := strings.TrimPrefix(strings.TrimSuffix(filepath.Base(partsFile), extParts), ".")
		if _, _, err := s.getTorrent(infoHash); err != nil {
			log.Debugf("Cleaning up stale parts file '%s'", partsFiles)
			deleteFile(partsFile)
		}
	}
}

func (s *Service) downloadProgress() {
	defer s.wg.Done()
	progressTicker := time.NewTicker(libtorrentProgressTime)
	defer progressTicker.Stop()

	for {
		select {
		case <-s.closing:
			return
		case <-progressTicker.C:
			if s.session.IsPaused() {
				continue
			}

			var totalDownloadRate int64
			var totalUploadRate int64
			var totalProgressSize float64
			var totalSize int64

			hasFilesBuffering := false
			bufferStateChanged := false

			s.mu.RLock()

			for _, t := range s.torrents {
				if t.isPaused || !t.hasMetadata || !t.handle.IsValid() {
					continue
				}

				for _, f := range t.files {
					if f.isBuffering {
						f.mu.Lock()
						if f.bufferBytesMissing() == 0 {
							f.isBuffering = false
							bufferStateChanged = true
						} else {
							hasFilesBuffering = true
						}
						f.mu.Unlock()
					}
				}

				torrentStatus := t.handle.Status(libtorrent.TorrentHandleQueryName)
				totalDownloadRate += int64(torrentStatus.GetDownloadRate())
				totalUploadRate += int64(torrentStatus.GetUploadRate())

				progress := float64(torrentStatus.GetProgress())
				if progress < 1 {
					size := torrentStatus.GetTotalWanted()
					totalProgressSize += progress * float64(size)
					totalSize += size
				} else {
					seedingTime := torrentStatus.GetSeedingDuration()
					if progress == 1 && seedingTime == 0 {
						seedingTime = torrentStatus.GetFinishedDuration()
					}
					downloadTime := torrentStatus.GetActiveDuration() - seedingTime
					allTimeDownload := torrentStatus.GetAllTimeDownload()

					if s.config.SeedTimeLimit > 0 && seedingTime >= int64(s.config.SeedTimeLimit) {
						log.Infof("Seeding time limit reached, pausing %s", torrentStatus.GetName())
						t.Pause()
					} else if s.config.SeedTimeRatioLimit > 0 && downloadTime > 0 &&
						seedingTime*100/downloadTime >= int64(s.config.SeedTimeRatioLimit) {
						log.Infof("Seeding time ratio reached, pausing %s", torrentStatus.GetName())
						t.Pause()
					} else if s.config.ShareRatioLimit > 0 && allTimeDownload > 0 &&
						torrentStatus.GetAllTimeUpload()*100/allTimeDownload >= int64(s.config.ShareRatioLimit) {
						log.Infof("Share ratio reached, pausing %s", torrentStatus.GetName())
						t.Pause()
					}
				}

				libtorrent.DeleteTorrentStatus(torrentStatus)
			}

			if bufferStateChanged && !hasFilesBuffering {
				s.setBufferingRateLimit(true)
			}

			s.downloadRate = totalDownloadRate
			s.uploadRate = totalUploadRate
			if totalSize > 0 {
				s.progress = 100 * totalProgressSize / float64(totalSize)
			} else {
				s.progress = 100
			}

			s.mu.RUnlock()
		}
	}
}

func (s *Service) Pause() {
	s.session.Pause()
}

func (s *Service) Resume() {
	s.session.Resume()
}

func (s *Service) GetStatus() *ServiceStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return &ServiceStatus{
		Progress:     s.progress,
		DownloadRate: s.downloadRate,
		UploadRate:   s.uploadRate,
		NumTorrents:  len(s.torrents),
		IsPaused:     s.session.IsPaused(),
	}
}

func (s *Service) Torrents() []*Torrent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	torrents := make([]*Torrent, len(s.torrents))
	copy(torrents, s.torrents)
	return torrents
}

func (s *Service) getTorrent(infoHash string) (int, *Torrent, error) {
	for i, t := range s.torrents {
		if t.infoHash == infoHash {
			return i, t, nil
		}
	}
	return -1, nil, InvalidInfoHashError
}

func (s *Service) GetTorrent(infoHash string) (*Torrent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, t, e := s.getTorrent(infoHash)
	return t, e
}

func (s *Service) RemoveTorrent(infoHash string, removeFiles bool) error {
	log.Debugf("Removing torrent with infohash %s and removeFiles=%t", infoHash, removeFiles)
	s.mu.Lock()
	defer s.mu.Unlock()

	index, torrent, err := s.getTorrent(infoHash)
	if err == nil {
		s.deletePartsFile(infoHash)
		s.deleteFastResumeFile(infoHash)
		s.deleteTorrentFile(infoHash)
		s.deleteMagnetFile(infoHash)
		s.torrents = append(s.torrents[:index], s.torrents[index+1:]...)
		torrent.remove(removeFiles)
	}

	return err
}

func (s *Service) partsFilePath(infoHash string) string {
	return filepath.Join(s.config.DownloadPath, "."+infoHash+extParts)
}

func (s *Service) deletePartsFile(infoHash string) {
	deleteFile(s.partsFilePath(infoHash))
}

func (s *Service) fastResumeFilePath(infoHash string) string {
	return filepath.Join(s.config.TorrentsPath, infoHash+extFastResume)
}

func (s *Service) deleteFastResumeFile(infoHash string) {
	deleteFile(s.fastResumeFilePath(infoHash))
}

func (s *Service) torrentPath(infoHash string) string {
	return filepath.Join(s.config.TorrentsPath, infoHash+extTorrent)
}

func (s *Service) deleteTorrentFile(infoHash string) {
	deleteFile(s.torrentPath(infoHash))
}

func (s *Service) magnetFilePath(infoHash string) string {
	return filepath.Join(s.config.TorrentsPath, infoHash+extMagnet)
}

func (s *Service) deleteMagnetFile(infoHash string) {
	deleteFile(s.magnetFilePath(infoHash))
}
