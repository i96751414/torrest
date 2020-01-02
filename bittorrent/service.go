package bittorrent

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/i96751414/libtorrent-go"
	"github.com/i96751414/torrest/settings"
	"github.com/i96751414/torrest/util"
	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("bittorrent")

const (
	libtorrentAlertWaitTime = 1
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
	torrents     map[string]*Torrent
	mu           *sync.RWMutex
	closing      chan interface{}
	UserAgent    string
	downloadRate int64
	uploadRate   int64
	progress     int
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

func (s *Service) AddMagnet(magnet string) (infoHash string, err error) {
	torrentParams := libtorrent.NewAddTorrentParams()
	defer libtorrent.DeleteAddTorrentParams(torrentParams)
	errorCode := libtorrent.NewErrorCode()
	defer libtorrent.DeleteErrorCode(errorCode)

	libtorrent.ParseMagnetUri(magnet, torrentParams, errorCode)
	if errorCode.Failed() {
		return "", errors.New(errorCode.Message().(string))
	}

	torrentParams.SetSavePath(s.config.DownloadPath)
	// Make sure we do not download anything yet
	filesPriorities := libtorrent.NewStdVectorChar()
	defer libtorrent.DeleteStdVectorChar(filesPriorities)
	for i := maxFilesPerTorrent; i > 0; i-- {
		filesPriorities.Add(0)
	}
	torrentParams.SetFilePriorities(filesPriorities)

	shaHash := torrentParams.GetInfoHash().ToString()
	infoHash = hex.EncodeToString([]byte(shaHash))

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.torrents[infoHash]; exists {
		err = errors.New("magnet was previously added")
	} else {
		torrentHandle := s.session.GetHandle().AddTorrent(torrentParams)
		if torrentHandle == nil || !torrentHandle.IsValid() {
			log.Errorf("Error adding magnet %s", magnet)
			err = errors.New("failed loading magnet")
		} else {
			s.torrents[infoHash] = NewTorrent(s, torrentHandle)
		}
	}

	return
}

func (s *Service) alertsConsumer() {
	ltTimer := libtorrent.Seconds(libtorrentAlertWaitTime)
	log.Info("Consuming alerts...")
	for {
		select {
		case <-s.closing:
			return
		default:
			if s.session.GetHandle().WaitForAlert(ltTimer).Swigcptr() == 0 {
				continue
			}

			alerts := s.session.GetHandle().PopAlerts()
			for i := 0; i < int(alerts.Size()); i++ {
				ltAlert := alerts.Get(i)
				alertType := ltAlert.Type()
				alertPtr := ltAlert.Swigcptr()
				alertMessage := ltAlert.Message()
				category := ltAlert.Category()
				what := ltAlert.What()

				switch alertType {
				case libtorrent.SaveResumeDataAlertAlertType:
					saveResumeData := libtorrent.SwigcptrSaveResumeDataAlert(alertPtr)
					torrentHandle := saveResumeData.GetHandle()
					torrentStatus := torrentHandle.Status(uint(libtorrent.TorrentHandleQuerySavePath) |
						uint(libtorrent.TorrentHandleQueryName))
					name := torrentStatus.GetName()
					shaHash := torrentStatus.GetInfoHash().ToString()
					infoHash := hex.EncodeToString([]byte(shaHash))

					bEncoded := []byte(libtorrent.Bencode(saveResumeData.ResumeData()))
					if _, err := DecodeTorrentData(bEncoded); err != nil {
						log.Warningf("Resume data corrupted for %s, %d bytes received and failed to decode with: %s, skipping...", name, len(bEncoded), err)
					} else {
						path := filepath.Join(s.config.TorrentsPath, fmt.Sprintf("%s.fastresume", infoHash))
						if err := ioutil.WriteFile(path, bEncoded, 0644); err != nil {
							log.Errorf("Failed saving '%s.fastresume': %s", infoHash, err)
						}
					}

				case libtorrent.ExternalIpAlertAlertType:
					splitMessage := strings.Split(alertMessage, ":")
					splitIP := strings.Split(splitMessage[len(splitMessage)-1], ".")
					alertMessage = strings.Join(splitMessage[:len(splitMessage)-1], ":") + splitIP[0] + ".XX.XX.XX"

				case libtorrent.MetadataReceivedAlertAlertType:
					metadataAlert := libtorrent.SwigcptrMetadataReceivedAlert(alertPtr)
					torrentHandle := metadataAlert.GetHandle()
					torrentStatus := torrentHandle.Status(uint(libtorrent.TorrentHandleQueryName))
					shaHash := torrentStatus.GetInfoHash().ToString()
					infoHash := hex.EncodeToString([]byte(shaHash))
					torrentFileName := filepath.Join(s.config.TorrentsPath, fmt.Sprintf("%s.torrent", infoHash))

					// Save .torrent
					log.Infof("Saving %s...", torrentFileName)
					torrentInfo := torrentHandle.TorrentFile()
					torrentFile := libtorrent.NewCreateTorrent(torrentInfo)
					torrentContent := torrentFile.Generate()
					bEncodedTorrent := []byte(libtorrent.Bencode(torrentContent))
					if err := ioutil.WriteFile(torrentFileName, bEncodedTorrent, 0644); err != nil {
						log.Errorf("Failed saving '%s.torrent': %s", infoHash, err)
					}
					libtorrent.DeleteCreateTorrent(torrentFile)

				case libtorrent.StateChangedAlertAlertType:
					stateAlert := libtorrent.SwigcptrStateChangedAlert(alertPtr)
					s.onStateChanged(stateAlert)
				}

				// log alerts
				if category&int(libtorrent.AlertErrorNotification) != 0 {
					log.Errorf("%s: %s", what, alertMessage)
				} else if category&int(libtorrent.AlertDebugNotification) != 0 {
					log.Debugf("%s: %s", what, alertMessage)
				} else if category&int(libtorrent.AlertPerformanceWarning) != 0 {
					log.Warningf("%s: %s", what, alertMessage)
				} else {
					log.Noticef("%s: %s", what, alertMessage)
				}
			}
		}
	}
}

func (s *Service) onStateChanged(stateAlert libtorrent.StateChangedAlert) {
	switch stateAlert.GetState() {
	case libtorrent.TorrentStatusDownloading:
		torrentHandle := stateAlert.GetHandle()
		torrentStatus := torrentHandle.Status(uint(libtorrent.TorrentHandleQueryName))
		shaHash := torrentStatus.GetInfoHash().ToString()
		infoHash := hex.EncodeToString([]byte(shaHash))
		if torrent, exists := s.torrents[infoHash]; exists {
			torrent.checkAvailableSpace()
		}
	}
}

func (s *Service) saveResumeDataLoop() {
	saveResumeWait := time.NewTicker(time.Duration(s.config.SessionSave) * time.Second)
	defer saveResumeWait.Stop()

	for {
		select {
		case <-saveResumeWait.C:
			s.mu.Lock()
			for _, torrent := range s.torrents {
				if torrent.handle.IsValid() {
					status := torrent.handle.Status()
					if status.GetHasMetadata() && status.GetNeedSaveResume() {
						torrent.handle.SaveResumeData(1)
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
	s.torrents = make(map[string]*Torrent)
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
		s.settingsPack.SetStr(libtorrent.SettingByName("user_agent"), s.UserAgent)
	}
	s.settingsPack.SetInt(libtorrent.SettingByName("request_timeout"), 2)
	s.settingsPack.SetInt(libtorrent.SettingByName("peer_connect_timeout"), 2)
	s.settingsPack.SetBool(libtorrent.SettingByName("strict_end_game_mode"), true)
	s.settingsPack.SetBool(libtorrent.SettingByName("announce_to_all_trackers"), true)
	s.settingsPack.SetBool(libtorrent.SettingByName("announce_to_all_tiers"), true)
	s.settingsPack.SetInt(libtorrent.SettingByName("connection_speed"), 500)
	s.settingsPack.SetInt(libtorrent.SettingByName("download_rate_limit"), 0)
	s.settingsPack.SetInt(libtorrent.SettingByName("upload_rate_limit"), 0)
	s.settingsPack.SetInt(libtorrent.SettingByName("choking_algorithm"), 0)
	s.settingsPack.SetInt(libtorrent.SettingByName("share_ratio_limit"), 0)
	s.settingsPack.SetInt(libtorrent.SettingByName("seed_time_ratio_limit"), 0)
	s.settingsPack.SetInt(libtorrent.SettingByName("seed_time_limit"), 0)
	s.settingsPack.SetInt(libtorrent.SettingByName("peer_tos"), ipToSLowCost)
	s.settingsPack.SetInt(libtorrent.SettingByName("torrent_connect_boost"), 0)
	s.settingsPack.SetBool(libtorrent.SettingByName("rate_limit_ip_overhead"), true)
	s.settingsPack.SetBool(libtorrent.SettingByName("no_atime_storage"), true)
	s.settingsPack.SetBool(libtorrent.SettingByName("announce_double_nat"), true)
	s.settingsPack.SetBool(libtorrent.SettingByName("prioritize_partial_pieces"), false)
	s.settingsPack.SetBool(libtorrent.SettingByName("free_torrent_hashes"), true)
	s.settingsPack.SetBool(libtorrent.SettingByName("use_parole_mode"), true)
	s.settingsPack.SetInt(libtorrent.SettingByName("seed_choking_algorithm"), int(libtorrent.SettingsPackFastestUpload))
	s.settingsPack.SetBool(libtorrent.SettingByName("upnp_ignore_nonrouters"), true)
	s.settingsPack.SetBool(libtorrent.SettingByName("lazy_bitfields"), true)
	s.settingsPack.SetInt(libtorrent.SettingByName("stop_tracker_timeout"), 1)
	s.settingsPack.SetInt(libtorrent.SettingByName("auto_scrape_interval"), 1200)
	s.settingsPack.SetInt(libtorrent.SettingByName("auto_scrape_min_interval"), 900)
	s.settingsPack.SetBool(libtorrent.SettingByName("ignore_limits_on_local_network"), true)
	s.settingsPack.SetBool(libtorrent.SettingByName("rate_limit_utp"), true)
	s.settingsPack.SetInt(libtorrent.SettingByName("mixed_mode_algorithm"), int(libtorrent.SettingsPackPreferTcp))

	// For Android external storage / OS-mounted NAS setups
	if s.config.TunedStorage {
		s.settingsPack.SetBool(libtorrent.SettingByName("use_read_cache"), true)
		s.settingsPack.SetBool(libtorrent.SettingByName("coalesce_reads"), true)
		s.settingsPack.SetBool(libtorrent.SettingByName("coalesce_writes"), true)
		s.settingsPack.SetInt(libtorrent.SettingByName("max_queued_disk_bytes"), 10*1024*1024)
		s.settingsPack.SetInt(libtorrent.SettingByName("cache_size"), -1)
	}

	if s.config.ConnectionsLimit > 0 {
		s.settingsPack.SetInt(libtorrent.SettingByName("connections_limit"), s.config.ConnectionsLimit)
	} else {
		setPlatformSpecificSettings(s.settingsPack)
	}

	if !s.config.LimitAfterBuffering {
		if s.config.MaxDownloadRate > 0 {
			log.Infof("Rate limiting download to %dkB/s", s.config.MaxDownloadRate/1024)
			s.settingsPack.SetInt(libtorrent.SettingByName("download_rate_limit"), s.config.MaxDownloadRate)
		}
		if s.config.MaxUploadRate > 0 {
			log.Infof("Rate limiting upload to %dkB/s", s.config.MaxUploadRate/1024)
			// If we have an upload rate, use the nicer bittyrant choker
			s.settingsPack.SetInt(libtorrent.SettingByName("upload_rate_limit"), s.config.MaxUploadRate)
			s.settingsPack.SetInt(libtorrent.SettingByName("choking_algorithm"), int(libtorrent.SettingsPackBittyrantChoker))
		}
	}

	if s.config.ShareRatioLimit > 0 {
		s.settingsPack.SetInt(libtorrent.SettingByName("share_ratio_limit"), s.config.ShareRatioLimit)
	}
	if s.config.SeedTimeRatioLimit > 0 {
		s.settingsPack.SetInt(libtorrent.SettingByName("seed_time_ratio_limit"), s.config.SeedTimeRatioLimit)
	}
	if s.config.SeedTimeLimit > 0 {
		s.settingsPack.SetInt(libtorrent.SettingByName("seed_time_limit"), s.config.SeedTimeLimit)
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

		s.settingsPack.SetInt(libtorrent.SettingByName("out_enc_policy"), policy)
		s.settingsPack.SetInt(libtorrent.SettingByName("in_enc_policy"), policy)
		s.settingsPack.SetInt(libtorrent.SettingByName("allowed_enc_level"), level)
		s.settingsPack.SetBool(libtorrent.SettingByName("prefer_rc4"), preferRc4)
	} else if s.config.EncryptionPolicy != settings.EncryptionEnabledPolicy {
		log.Warning("Invalid encryption policy provided. Using default")
	}

	if s.config.Proxy != nil && s.config.Proxy.Type != settings.ProxyTypeNone {
		log.Info("Applying proxy settings...")
		s.settingsPack.SetInt(libtorrent.SettingByName("proxy_type"), s.config.Proxy.Type)
		s.settingsPack.SetInt(libtorrent.SettingByName("proxy_port"), s.config.Proxy.Port)
		s.settingsPack.SetStr(libtorrent.SettingByName("proxy_hostname"), s.config.Proxy.Hostname)
		s.settingsPack.SetStr(libtorrent.SettingByName("proxy_username"), s.config.Proxy.Username)
		s.settingsPack.SetStr(libtorrent.SettingByName("proxy_password"), s.config.Proxy.Password)
		s.settingsPack.SetBool(libtorrent.SettingByName("proxy_tracker_connections"), true)
		s.settingsPack.SetBool(libtorrent.SettingByName("proxy_peer_connections"), true)
		s.settingsPack.SetBool(libtorrent.SettingByName("proxy_hostnames"), true)
		s.settingsPack.SetBool(libtorrent.SettingByName("force_proxy"), true)
		if s.config.Proxy.Type == settings.ProxyTypeI2PSAM {
			s.settingsPack.SetInt(libtorrent.SettingByName("i2p_port"), s.config.Proxy.Port)
			s.settingsPack.SetStr(libtorrent.SettingByName("i2p_hostname"), s.config.Proxy.Hostname)
			s.settingsPack.SetBool(libtorrent.SettingByName("allows_i2p_mixed"), false)
			s.settingsPack.SetBool(libtorrent.SettingByName("allows_i2p_mixed"), true)
		}
	}

	// Set alert_mask here so it also applies on reconfigure...
	s.settingsPack.SetInt(libtorrent.SettingByName("alert_mask"), int(
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
	s.settingsPack.SetStr(libtorrent.SettingByName("listen_interfaces"), strings.Join(listenInterfacesStrings, ","))

	outgoingInterfaces := strings.TrimSpace(s.config.OutgoingInterfaces)
	if outgoingInterfaces != "" {
		s.settingsPack.SetStr(libtorrent.SettingByName("outgoing_interfaces"), strings.Replace(outgoingInterfaces, " ", "", -1))
	}

	log.Info("Starting LSD...")
	s.settingsPack.SetBool(libtorrent.SettingByName("enable_lsd"), true)

	if !s.config.DisableDHT {
		log.Info("Starting DHT...")
		s.settingsPack.SetStr(libtorrent.SettingByName("dht_bootstrap_nodes"), strings.Join([]string{
			"router.utorrent.com:6881",
			"router.bittorrent.com:6881",
			"dht.transmissionbt.com:6881",
			"dht.aelitis.com:6881",     // Vuze
			"router.silotis.us:6881",   // IPv6
			"dht.libtorrent.org:25401", // @arvidn's
		}, ","))
		s.settingsPack.SetBool(libtorrent.SettingByName("enable_dht"), true)
	}

	if !s.config.DisableUPNP {
		log.Info("Starting UPNP...")
		s.settingsPack.SetBool(libtorrent.SettingByName("enable_upnp"), true)

		log.Info("Starting NATPMP...")
		s.settingsPack.SetBool(libtorrent.SettingByName("enable_natpmp"), true)
	}

	s.session = libtorrent.NewSession(s.settingsPack, int(libtorrent.SessionHandleAddDefaultPlugins))
}

func (s *Service) setBufferingRateLimit(enable bool) {
	if s.config.LimitAfterBuffering {
		if enable {
			if s.config.MaxDownloadRate > 0 {
				log.Infof("Buffer filled, rate limiting download to %dkB/s", s.config.MaxDownloadRate/1024)
				s.settingsPack.SetInt(libtorrent.SettingByName("download_rate_limit"), s.config.MaxDownloadRate)
			}
			if s.config.MaxUploadRate > 0 {
				// If we have an upload rate, use the nicer bittyrant choker
				log.Infof("Buffer filled, rate limiting upload to %dkB/s", s.config.MaxUploadRate/1024)
				s.settingsPack.SetInt(libtorrent.SettingByName("upload_rate_limit"), s.config.MaxUploadRate)
			}
		} else {
			log.Info("Resetting rate limiting")
			s.settingsPack.SetInt(libtorrent.SettingByName("download_rate_limit"), 0)
			s.settingsPack.SetInt(libtorrent.SettingByName("upload_rate_limit"), 0)
		}
		s.session.GetHandle().ApplySettings(s.settingsPack)
	}
}

func (s *Service) stopServices() {
	log.Info("Stopping LSD...")
	s.settingsPack.SetBool(libtorrent.SettingByName("enable_lsd"), false)

	if !s.config.DisableDHT {
		log.Info("Stopping DHT...")
		s.settingsPack.SetBool(libtorrent.SettingByName("enable_dht"), false)
	}

	if !s.config.DisableUPNP {
		log.Info("Stopping UPNP...")
		s.settingsPack.SetBool(libtorrent.SettingByName("enable_upnp"), false)

		log.Info("Stopping NATPMP...")
		s.settingsPack.SetBool(libtorrent.SettingByName("enable_natpmp"), false)
	}

	s.session.GetHandle().ApplySettings(s.settingsPack)
}

func (s *Service) AddTorrentFile(torrentFile string) (infoHash string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	log.Infof("Loading torrent file %s", torrentFile)

	torrentParams := libtorrent.NewAddTorrentParams()
	defer libtorrent.DeleteAddTorrentParams(torrentParams)
	info := libtorrent.NewTorrentInfo(torrentFile)
	defer libtorrent.DeleteTorrentInfo(info)

	torrentParams.SetTorrentInfo(info)
	torrentParams.SetSavePath(s.config.DownloadPath)
	// Make sure we do not download anything yet
	filesPriorities := libtorrent.NewStdVectorChar()
	defer libtorrent.DeleteStdVectorChar(filesPriorities)
	for i := maxFilesPerTorrent; i > 0; i-- {
		filesPriorities.Add(0)
	}
	torrentParams.SetFilePriorities(filesPriorities)

	shaHash := info.InfoHash().ToString()
	infoHash = hex.EncodeToString([]byte(shaHash))
	fastResumeFile := s.fastResumeFilePath(infoHash)

	if _, e := os.Stat(fastResumeFile); e == nil {
		if fastResumeData, e := ioutil.ReadFile(fastResumeFile); e != nil {
			log.Errorf("Error reading fastresume file: %s", e)
			deleteFile(fastResumeFile)
		} else {
			fastResumeVector := libtorrent.NewStdVectorChar()
			defer libtorrent.DeleteStdVectorChar(fastResumeVector)
			for _, c := range fastResumeData {
				fastResumeVector.Add(c)
			}
			torrentParams.SetResumeData(fastResumeVector)
		}
	}

	if _, exists := s.torrents[infoHash]; exists {
		err = errors.New("torrent was previously added")
	} else {
		torrentHandle := s.session.GetHandle().AddTorrent(torrentParams)
		if torrentHandle == nil || !torrentHandle.IsValid() {
			log.Errorf("Error adding torrent file for %s", torrentFile)
			err = errors.New("failed loading torrent")
			s.deletePartsFile(infoHash)
			s.deleteFastResumeFile(infoHash)
			s.deleteTorrentFile(infoHash)
		} else {
			torrentDst := s.torrentPath(infoHash)
			fi1, e1 := os.Stat(torrentFile)
			fi2, e2 := os.Stat(torrentDst)
			if e1 != nil || e2 != nil || !os.SameFile(fi1, fi2) {
				log.Infof("Copying torrent %s", infoHash)
				if err := copyFileContents(torrentFile, torrentDst); err != nil {
					log.Errorf("Failed copying torrent: %s", err)
				}
			}
			s.torrents[infoHash] = NewTorrent(s, torrentHandle)
		}
	}

	return
}

func (s *Service) loadTorrentFiles() {
	files, _ := filepath.Glob(filepath.Join(s.config.TorrentsPath, "*.torrent"))
	for _, torrentFile := range files {
		_, _ = s.AddTorrentFile(torrentFile)
	}

	log.Info("Cleaning up stale .parts files...")
	partsFiles, _ := filepath.Glob(filepath.Join(s.config.DownloadPath, "*.parts"))
	for _, partsFile := range partsFiles {
		infoHash := strings.TrimPrefix(strings.TrimSuffix(filepath.Base(partsFile), ".parts"), ".")
		if _, exists := s.torrents[infoHash]; !exists {
			deleteFile(partsFile)
		}
	}
}

func (s *Service) downloadProgress() {
	progressTicker := time.NewTicker(libtorrentProgressTime * time.Second)
	defer progressTicker.Stop()

	for {
		select {
		case <-progressTicker.C:
			if s.session.GetHandle().IsPaused() {
				continue
			}

			var totalDownloadRate int64
			var totalUploadRate int64
			var totalProgress int

			hasFilesBuffering := false
			bufferStateChanged := false

			s.mu.Lock()

			for _, t := range s.torrents {
				if t.isPaused || !t.handle.IsValid() {
					continue
				}

				torrentStatus := t.handle.Status(uint(libtorrent.TorrentHandleQueryName))
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
				progress := int(float64(torrentStatus.GetProgress()) * 100)

				if progress < 100 {
					totalProgress += progress
					continue
				}

				seedingTime := torrentStatus.GetSeedingTime()
				finishedTime := torrentStatus.GetFinishedTime()
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
					if downloadTime := torrentStatus.GetActiveTime() - seedingTime; downloadTime > 1 {
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
			s.progress = totalProgress
			s.mu.Unlock()
		}
	}
}

func (s *Service) RemoveTorrent(infoHash string, removeFiles bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if torrent, exists := s.torrents[infoHash]; exists {
		s.deletePartsFile(infoHash)
		s.deleteFastResumeFile(infoHash)
		s.deleteTorrentFile(infoHash)

		var flags int
		if removeFiles {
			flags |= int(libtorrent.SessionHandleDeleteFiles)
		}
		s.session.GetHandle().RemoveTorrent(torrent.handle, flags)
		delete(s.torrents, infoHash)
		return true
	}

	return false
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
