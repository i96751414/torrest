package bittorrent

import (
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/i96751414/libtorrent-go"
	"github.com/i96751414/torrest/settings"
	"github.com/i96751414/torrest/util"
	"github.com/op/go-logging"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var log = logging.MustGetLogger("bittorrent")

const (
	libtorrentAlertWaitTime = time.Second
	libtorrentProgressTime  = 1
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

// Service represents the torrent service
type Service struct {
	session      libtorrent.Session
	config       *settings.Settings
	settingsPack libtorrent.SettingsPack
	torrents     []*Torrent
	mu           *sync.RWMutex
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
}

// NewService creates a service given the provided configs
func NewService(config *settings.Settings) *Service {
	s := &Service{
		config:  config,
		mu:      &sync.RWMutex{},
		closing: make(chan interface{}),
	}

	createDir(s.config.DownloadPath)
	createDir(s.config.TorrentsPath)

	s.configure()
	s.loadTorrentFiles()

	go s.saveResumeDataLoop()
	go s.alertsConsumer()
	go s.downloadProgress()

	return s
}

func (s *Service) alertsConsumer() {
	ipRegex := regexp.MustCompile(`\.\d+`)
	log.Info("Consuming alerts...")
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
				if category&libtorrent.AlertErrorNotification != 0 {
					log.Errorf("%s: %s", what, alertMessage)
				} else if category&libtorrent.AlertConnectNotification != 0 {
					log.Debugf("%s: %s", what, alertMessage)
				} else if category&libtorrent.AlertPerformanceWarning != 0 {
					log.Warningf("%s: %s", what, alertMessage)
				} else {
					log.Noticef("%s: %s", what, alertMessage)
				}
			}
			libtorrent.DeleteStdVectorAlerts(alerts)
		}
	}
}

func (s *Service) onSaveResumeData(alert libtorrent.SaveResumeDataAlert) {
	torrentHandle := alert.GetHandle()
	torrentStatus := torrentHandle.Status(libtorrent.TorrentHandleQuerySavePath |
		libtorrent.TorrentHandleQueryName)
	infoHash := hex.EncodeToString([]byte(torrentStatus.GetInfoHash().ToString()))

	params := alert.GetParams()
	entry := libtorrent.WriteResumeData(params)
	defer libtorrent.DeleteEntry(entry)

	bEncoded := []byte(libtorrent.Bencode(entry))
	if _, err := DecodeTorrentData(bEncoded); err == nil {
		if err := ioutil.WriteFile(s.fastResumeFilePath(infoHash), bEncoded, 0644); err != nil {
			log.Errorf("Failed saving '%s.fastresume': %s", infoHash, err)
		}
	} else {
		log.Warningf("Resume data corrupted for %s, %d bytes received and failed to decode with: %s, skipping...",
			torrentStatus.GetName(), len(bEncoded), err)
	}
}

func (s *Service) onMetadataReceived(alert libtorrent.MetadataReceivedAlert) {
	torrentHandle := alert.GetHandle()
	torrentStatus := torrentHandle.Status(libtorrent.TorrentHandleQueryName)
	infoHash := hex.EncodeToString([]byte(torrentStatus.GetInfoHash().ToString()))

	// Save .torrent
	log.Infof("Saving %s.torrent", infoHash)
	torrentInfo := torrentHandle.TorrentFile()
	torrentFile := libtorrent.NewCreateTorrent(torrentInfo)
	torrentContent := torrentFile.Generate()
	bEncodedTorrent := []byte(libtorrent.Bencode(torrentContent))
	if err := ioutil.WriteFile(s.torrentPath(infoHash), bEncodedTorrent, 0644); err != nil {
		log.Errorf("Failed saving '%s.torrent': %s", infoHash, err)
	}
	libtorrent.DeleteCreateTorrent(torrentFile)
}

func (s *Service) onStateChanged(alert libtorrent.StateChangedAlert) {
	switch alert.GetState() {
	case libtorrent.TorrentStatusDownloading:
		torrentHandle := alert.GetHandle()
		torrentStatus := torrentHandle.Status(libtorrent.TorrentHandleQueryName)
		infoHash := hex.EncodeToString([]byte(torrentStatus.GetInfoHash().ToString()))
		if _, torrent, err := s.getTorrent(infoHash); err == nil {
			torrent.checkAvailableSpace()
		}
	}
}

func (s *Service) saveResumeDataLoop() {
	saveResumeWait := time.NewTicker(time.Duration(s.config.SessionSave) * time.Second)
	defer saveResumeWait.Stop()

	for {
		select {
		case <-s.closing:
			return
		case <-saveResumeWait.C:
			s.mu.Lock()
			for _, torrent := range s.torrents {
				if torrent.handle.IsValid() {
					status := torrent.handle.Status()
					if status.GetHasMetadata() && status.GetNeedSaveResume() {
						torrent.handle.SaveResumeData(libtorrent.TorrentHandleSaveInfoDict)
					}
				}
			}
			s.mu.Unlock()
		}
	}
}

func (s *Service) Close() {
	log.Info("Stopping Service...")
	s.stopServices()
	close(s.closing)
	libtorrent.DeleteSession(s.session)
}

func (s *Service) Reconfigure(config *settings.Settings) {
	s.stopServices()
	s.config = config
	s.configure()
	s.loadTorrentFiles()
}

func (s *Service) configure() {
	s.torrents = nil
	s.settingsPack = libtorrent.NewSettingsPack()

	log.Info("Applying session settings...")

	s.UserAgent = util.UserAgent()
	if s.config.UserAgent > 0 {
		switch s.config.UserAgent {
		case settings.LibtorrentUA:
			s.UserAgent = fmt.Sprintf("libtorrent/%s", libtorrent.Version())
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
			log.Warning("Invalid user agent provided. Using default.")
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
		if s.config.MaxDownloadRate > 0 {
			log.Infof("Rate limiting download to %dkB/s", s.config.MaxDownloadRate/1024)
			s.settingsPack.SetInt("download_rate_limit", s.config.MaxDownloadRate)
		}
		if s.config.MaxUploadRate > 0 {
			log.Infof("Rate limiting upload to %dkB/s", s.config.MaxUploadRate/1024)
			// If we have an upload rate, use the nicer bittyrant choker
			s.settingsPack.SetInt("upload_rate_limit", s.config.MaxUploadRate)
			s.settingsPack.SetInt("choking_algorithm", int(libtorrent.SettingsPackBittyrantChoker))
		}
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

	log.Info("Applying encryption settings...")
	if s.config.EncryptionPolicy == settings.EncryptionDisabledPolicy || s.config.EncryptionPolicy == settings.EncryptionForcedPolicy {
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
		log.Info("Applying proxy settings...")
		s.settingsPack.SetInt("proxy_type", s.config.Proxy.Type)
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
	var listenPorts []string
	for p := s.config.LowerListenPort; p <= s.config.UpperListenPort; p++ {
		listenPorts = append(listenPorts, strconv.Itoa(p))
	}
	if len(listenPorts) == 0 {
		panic("Invalid LowerListenPort/UpperListenPort configuration")
	}

	var listenInterfaces []string
	interfaces := strings.TrimSpace(s.config.ListenInterfaces)
	if interfaces != "" {
		listenInterfaces = strings.Split(strings.Replace(interfaces, " ", "", -1), ",")
	} else {
		listenInterfaces = []string{"0.0.0.0"}
	}

	// TODO: properly do this
	rand.Seed(time.Now().UTC().UnixNano())
	var listenInterfacesStrings []string
	for _, listenInterface := range listenInterfaces {
		listenInterfacesStrings = append(listenInterfacesStrings, listenInterface+":"+listenPorts[rand.Intn(len(listenPorts))])
		if len(listenPorts) > 1 {
			listenInterfacesStrings = append(listenInterfacesStrings, listenInterface+":"+listenPorts[rand.Intn(len(listenPorts))])
		}
	}
	s.settingsPack.SetStr("listen_interfaces", strings.Join(listenInterfacesStrings, ","))

	outgoingInterfaces := strings.TrimSpace(s.config.OutgoingInterfaces)
	if outgoingInterfaces != "" {
		s.settingsPack.SetStr("outgoing_interfaces", strings.Replace(outgoingInterfaces, " ", "", -1))
	}

	log.Info("Starting LSD...")
	s.settingsPack.SetBool("enable_lsd", true)

	if !s.config.DisableDHT {
		log.Info("Starting DHT...")
		s.settingsPack.SetStr("dht_bootstrap_nodes", strings.Join([]string{
			"router.utorrent.com:6881",
			"router.bittorrent.com:6881",
			"dht.transmissionbt.com:6881",
			"dht.aelitis.com:6881",     // Vuze
			"router.silotis.us:6881",   // IPv6
			"dht.libtorrent.org:25401", // @arvidn's
		}, ","))
		s.settingsPack.SetBool("enable_dht", true)
	}

	if !s.config.DisableUPNP {
		log.Info("Starting UPNP...")
		s.settingsPack.SetBool("enable_upnp", true)

		log.Info("Starting NATPMP...")
		s.settingsPack.SetBool("enable_natpmp", true)
	}

	s.session = libtorrent.NewSession(s.settingsPack, libtorrent.SessionHandleAddDefaultPlugins)
}

func (s *Service) setBufferingRateLimit(enable bool) {
	if s.config.LimitAfterBuffering {
		if enable {
			if s.config.MaxDownloadRate > 0 {
				log.Infof("Buffer filled, rate limiting download to %dkB/s", s.config.MaxDownloadRate/1024)
				s.settingsPack.SetInt("download_rate_limit", s.config.MaxDownloadRate)
			}
			if s.config.MaxUploadRate > 0 {
				// If we have an upload rate, use the nicer bittyrant choker
				log.Infof("Buffer filled, rate limiting upload to %dkB/s", s.config.MaxUploadRate/1024)
				s.settingsPack.SetInt("upload_rate_limit", s.config.MaxUploadRate)
			}
		} else {
			log.Info("Resetting rate limiting")
			s.settingsPack.SetInt("download_rate_limit", 0)
			s.settingsPack.SetInt("upload_rate_limit", 0)
		}
		s.session.ApplySettings(s.settingsPack)
	}
}

func (s *Service) stopServices() {
	log.Info("Stopping LSD...")
	s.settingsPack.SetBool("enable_lsd", false)

	if !s.config.DisableDHT {
		log.Info("Stopping DHT...")
		s.settingsPack.SetBool("enable_dht", false)
	}

	if !s.config.DisableUPNP {
		log.Info("Stopping UPNP...")
		s.settingsPack.SetBool("enable_upnp", false)

		log.Info("Stopping NATPMP...")
		s.settingsPack.SetBool("enable_natpmp", false)
	}

	s.session.ApplySettings(s.settingsPack)
}

func (s *Service) addTorrentWithParams(torrentParams libtorrent.AddTorrentParams, infoHash string, shouldStart bool) error {
	torrentParams.SetSavePath(s.config.DownloadPath)

	if !shouldStart {
		// Make sure we do not download anything yet
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
		torrentHandle := s.session.AddTorrent(torrentParams)
		if torrentHandle == nil || !torrentHandle.IsValid() {
			log.Errorf("Error adding torrent '%s'", infoHash)
			return LoadTorrentError
		} else {
			s.torrents = append(s.torrents, NewTorrent(s, torrentHandle, infoHash))
		}
	}
	return nil
}

func (s *Service) AddMagnet(magnet string) (infoHash string, err error) {
	torrentParams := libtorrent.NewAddTorrentParams()
	defer libtorrent.DeleteAddTorrentParams(torrentParams)
	errorCode := libtorrent.NewErrorCode()
	defer libtorrent.DeleteErrorCode(errorCode)

	libtorrent.ParseMagnetUri(magnet, torrentParams, errorCode)
	if errorCode.Failed() {
		return "", errors.New(errorCode.Message().(string))
	}

	infoHash = hex.EncodeToString([]byte(torrentParams.GetInfoHash().ToString()))

	s.mu.Lock()
	defer s.mu.Unlock()
	err = s.addTorrentWithParams(torrentParams, infoHash, false)
	return
}

func (s *Service) AddTorrentData(data []byte) (infoHash string, err error) {
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
	infoHash = hex.EncodeToString([]byte(info.InfoHash().ToString()))

	s.mu.Lock()
	defer s.mu.Unlock()
	err = s.addTorrentWithParams(torrentParams, infoHash, false)
	if err == nil {
		if e := ioutil.WriteFile(s.torrentPath(infoHash), data, 0644); e != nil {
			log.Errorf("Failed saving torrent: %s", err)
		}
	}
	return
}

func (s *Service) AddTorrentFile(torrentFile string) (infoHash string, err error) {
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
	infoHash = hex.EncodeToString([]byte(info.InfoHash().ToString()))

	s.mu.Lock()
	defer s.mu.Unlock()
	err = s.addTorrentWithParams(torrentParams, infoHash, false)
	if err == nil {
		torrentDst := s.torrentPath(infoHash)
		if fi2, e := os.Stat(torrentDst); e != nil || !os.SameFile(fi, fi2) {
			log.Infof("Copying torrent %s", infoHash)
			if err := copyFileContents(torrentFile, torrentDst); err != nil {
				log.Errorf("Failed copying torrent: %s", err)
			}
		}
	}
	return
}

func (s *Service) addTorrentWithResumeData(fastResumeFile string) (err error) {
	if fastResumeData, e := ioutil.ReadFile(fastResumeFile); e != nil {
		deleteFile(fastResumeFile)
		err = e
	} else {
		node := libtorrent.NewBdecodeNode()
		defer libtorrent.DeleteBdecodeNode(node)
		errorCode := libtorrent.Bdecode(string(fastResumeData), node)
		defer libtorrent.DeleteErrorCode(errorCode)
		if errorCode.Failed() {
			err = errors.New(errorCode.Message().(string))
		} else {
			torrentParams := libtorrent.ReadResumeData(node, errorCode)
			defer libtorrent.DeleteAddTorrentParams(torrentParams)
			if errorCode.Failed() {
				err = errors.New(errorCode.Message().(string))
			} else {
				infoHash := hex.EncodeToString([]byte(torrentParams.GetInfoHash().ToString()))
				err = s.addTorrentWithParams(torrentParams, infoHash, true)
			}
		}
	}
	return
}

func (s *Service) loadTorrentFiles() {
	resumeFiles, _ := filepath.Glob(filepath.Join(s.config.TorrentsPath, "*.fastresume"))
	for _, fastResumeFile := range resumeFiles {
		if err := s.addTorrentWithResumeData(fastResumeFile); err != nil {
			log.Errorf("Failed adding torrent with resume data: %s")
		}
	}

	files, _ := filepath.Glob(filepath.Join(s.config.TorrentsPath, "*.torrent"))
	for _, torrentFile := range files {
		if infoHash, err := s.AddTorrentFile(torrentFile); err == LoadTorrentError {
			s.deletePartsFile(infoHash)
			s.deleteFastResumeFile(infoHash)
			s.deleteTorrentFile(infoHash)
		}
	}

	log.Info("Cleaning up stale .parts files...")
	partsFiles, _ := filepath.Glob(filepath.Join(s.config.DownloadPath, "*.parts"))
	for _, partsFile := range partsFiles {
		infoHash := strings.TrimPrefix(strings.TrimSuffix(filepath.Base(partsFile), ".parts"), ".")
		if _, _, err := s.getTorrent(infoHash); err != nil {
			deleteFile(partsFile)
		}
	}
}

func (s *Service) downloadProgress() {
	progressTicker := time.NewTicker(libtorrentProgressTime * time.Second)
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
			var totalProgress float64
			var totalSize int64

			hasFilesBuffering := false
			bufferStateChanged := false

			s.mu.Lock()

			for _, t := range s.torrents {
				if t.isPaused || !t.handle.IsValid() {
					continue
				}

				torrentStatus := t.handle.Status(libtorrent.TorrentHandleQueryName)
				if !torrentStatus.GetHasMetadata() {
					continue
				}

				for _, f := range t.Files() {
					if f.isBuffering {
						if f.bufferBytesMissing() == 0 {
							f.isBuffering = false
							bufferStateChanged = true
						} else {
							hasFilesBuffering = true
						}
					}
				}

				totalDownloadRate += int64(torrentStatus.GetDownloadRate())
				totalUploadRate += int64(torrentStatus.GetUploadRate())
				progress := float64(torrentStatus.GetProgress())

				if progress < 100 {
					size := torrentStatus.GetTotalWanted()
					totalProgress += progress * float64(size)
					totalSize += size
					continue
				}

				seedingTime := torrentStatus.GetSeedingDuration()
				finishedTime := torrentStatus.GetFinishedDuration()
				if progress == 100 && seedingTime == 0 {
					seedingTime = finishedTime
				}

				if s.config.SeedTimeLimit > 0 {
					if seedingTime >= s.config.SeedTimeLimit {
						log.Warningf("Seeding time limit reached, pausing %s", torrentStatus.GetName())
						t.Pause()
						continue
					}
				}
				if s.config.SeedTimeRatioLimit > 0 {
					if downloadTime := torrentStatus.GetActiveDuration() - seedingTime; downloadTime > 1 {
						timeRatio := seedingTime * 100 / downloadTime
						if timeRatio >= s.config.SeedTimeRatioLimit {
							log.Warningf("Seeding time ratio reached, pausing %s", torrentStatus.GetName())
							t.Pause()
							continue
						}
					}
				}
				if s.config.ShareRatioLimit > 0 {
					if allTimeDownload := torrentStatus.GetAllTimeDownload(); allTimeDownload > 0 {
						ratio := torrentStatus.GetAllTimeUpload() * 100 / allTimeDownload
						if ratio >= int64(s.config.ShareRatioLimit) {
							log.Warningf("Share ratio reached, pausing %s", torrentStatus.GetName())
							t.Pause()
						}
					}
				}
			}

			if bufferStateChanged && !hasFilesBuffering {
				s.setBufferingRateLimit(true)
			}

			s.downloadRate = totalDownloadRate
			s.uploadRate = totalUploadRate
			if totalSize > 0 {
				s.progress = 100 * totalProgress / float64(totalSize)
			} else {
				s.progress = 100
			}

			s.mu.Unlock()
		}
	}
}

func (s *Service) GetStatus() *ServiceStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &ServiceStatus{
		Progress:     s.progress,
		DownloadRate: s.downloadRate,
		UploadRate:   s.uploadRate,
	}
}

func (s *Service) Torrents() []*Torrent {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	_, t, e := s.getTorrent(infoHash)
	return t, e
}

func (s *Service) RemoveTorrent(infoHash string, removeFiles bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	index, torrent, err := s.getTorrent(infoHash)
	if err == nil {
		s.deletePartsFile(infoHash)
		s.deleteFastResumeFile(infoHash)
		s.deleteTorrentFile(infoHash)

		var flags int
		if removeFiles {
			flags |= int(libtorrent.SessionHandleDeleteFiles)
		}
		s.session.RemoveTorrent(torrent.handle, flags)
		s.torrents = append(s.torrents[:index], s.torrents[index+1:]...)
	}

	return err
}

func (s *Service) partsFilePath(infoHash string) string {
	return filepath.Join(s.config.DownloadPath, fmt.Sprintf(".%s.parts", infoHash))
}

func (s *Service) deletePartsFile(infoHash string) {
	deleteFile(s.partsFilePath(infoHash))
}

func (s *Service) fastResumeFilePath(infoHash string) string {
	return filepath.Join(s.config.TorrentsPath, fmt.Sprintf("%s.fastresume", infoHash))
}

func (s *Service) deleteFastResumeFile(infoHash string) {
	deleteFile(s.fastResumeFilePath(infoHash))
}

func (s *Service) torrentPath(infoHash string) string {
	return filepath.Join(s.config.TorrentsPath, fmt.Sprintf("%s.torrent", infoHash))
}

func (s *Service) deleteTorrentFile(infoHash string) {
	deleteFile(s.torrentPath(infoHash))
}
