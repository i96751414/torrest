package settings

import (
	"encoding/json"
	"io/ioutil"
	"path/filepath"
	"time"

	"github.com/go-playground/validator"
	"github.com/jinzhu/copier"
	"github.com/op/go-logging"
)

var validate = validator.New()

type UserAgentType int

//noinspection GoSnakeCaseUsage
const (
	DefaultUA UserAgentType = iota
	LibtorrentUA
	LibtorrentRasterbar_1_1_0_UA
	BitTorrent_7_5_0_UA
	BitTorrent_7_4_3_UA
	UTorrent_3_4_9_UA
	UTorrent_3_2_0_UA
	UTorrent_2_2_1_UA
	Transmission_2_92_UA
	Deluge_1_3_6_0_UA
	Deluge_1_3_12_0_UA
	Vuze_5_7_3_0_UA
)

type EncryptionPolicy int

const (
	EncryptionEnabledPolicy EncryptionPolicy = iota
	EncryptionDisabledPolicy
	EncryptionForcedPolicy
)

type ProxyType int

//noinspection GoUnusedConst
const (
	ProxyTypeNone ProxyType = iota
	ProxyTypeSocks4
	ProxyTypeSocks5
	ProxyTypeSocks5Password
	ProxyTypeSocksHTTP
	ProxyTypeSocksHTTPPassword
	ProxyTypeI2PSAM
)

type ProxySettings struct {
	Type     ProxyType `json:"type" validate:"gte=0,lte=6"`
	Port     int       `json:"port" validate:"gte=0,lte=65535"`
	Hostname string    `json:"hostname"`
	Username string    `json:"username"`
	Password string    `json:"password"`
}

// Settings define the server settings
type Settings struct {
	settingsPath string `json:"-"`

	ListenPort           uint             `json:"listen_port" validate:"gte=0,lte=65535" example:"6889"`
	ListenInterfaces     string           `json:"listen_interfaces" example:""`
	OutgoingInterfaces   string           `json:"outgoing_interfaces" example:""`
	DisableDHT           bool             `json:"disable_dht" example:"false"`
	DisableUPNP          bool             `json:"disable_upnp" example:"false"`
	DisableNatPMP        bool             `json:"disable_natpmp" example:"false"`
	DisableLSD           bool             `json:"disable_lsd" example:"false"`
	DownloadPath         string           `json:"download_path" validate:"required" example:"downloads"`
	TorrentsPath         string           `json:"torrents_path" validate:"required" example:"downloads/torrents"`
	UserAgent            UserAgentType    `json:"user_agent" validate:"gte=0,lte=6" example:"0"`
	SessionSave          time.Duration    `json:"session_save" validate:"gt=0" example:"30" swaggertype:"integer"`
	TunedStorage         bool             `json:"tuned_storage" example:"false"`
	CheckAvailableSpace  bool             `json:"check_available_space" example:"true"`
	ConnectionsLimit     int              `json:"connections_limit" example:"200"`
	LimitAfterBuffering  bool             `json:"limit_after_buffering" example:"false"`
	MaxDownloadRate      int              `json:"max_download_rate" validate:"gte=0" example:"0"`
	MaxUploadRate        int              `json:"max_upload_rate" validate:"gte=0" example:"0"`
	ShareRatioLimit      int              `json:"share_ratio_limit" validate:"gte=0" example:"200"`
	SeedTimeRatioLimit   int              `json:"seed_time_ratio_limit" validate:"gte=0" example:"700"`
	SeedTimeLimit        int              `json:"seed_time_limit" validate:"gte=0" example:"86400"`
	ActiveDownloadsLimit int              `json:"active_downloads_limit" example:"3"`
	ActiveSeedsLimit     int              `json:"active_seeds_limit" example:"5"`
	ActiveCheckingLimit  int              `json:"active_checking_limit" example:"1"`
	ActiveDhtLimit       int              `json:"active_dht_limit" example:"88"`
	ActiveTrackerLimit   int              `json:"active_tracker_limit" example:"1600"`
	ActiveLsdLimit       int              `json:"active_lsd_limit" example:"60"`
	ActiveLimit          int              `json:"active_limit" example:"500"`
	EncryptionPolicy     EncryptionPolicy `json:"encryption_policy" validate:"gte=0,lte=2" example:"0"`
	Proxy                *ProxySettings   `json:"proxy"`
	BufferSize           int64            `json:"buffer_size" example:"20971520"`
	PieceWaitTimeout     time.Duration    `json:"piece_wait_timeout" validate:"gte=0" example:"60" swaggertype:"integer"`
	ServiceLogLevel      logging.Level    `json:"service_log_level" validate:"gte=0,lte=5" example:"4" swaggertype:"integer"`
	AlertsLogLevel       logging.Level    `json:"alerts_log_level" validate:"gte=0,lte=5" example:"0" swaggertype:"integer"`
	ApiLogLevel          logging.Level    `json:"api_log_level" validate:"gte=0,lte=5" example:"1" swaggertype:"integer"`
}

func DefaultSettings() *Settings {
	return &Settings{
		settingsPath:         "settings.json",
		ListenPort:           6889,
		ListenInterfaces:     "",
		OutgoingInterfaces:   "",
		DisableDHT:           false,
		DisableUPNP:          false,
		DisableNatPMP:        false,
		DisableLSD:           false,
		DownloadPath:         "downloads",
		TorrentsPath:         filepath.Join("downloads", "torrents"),
		UserAgent:            DefaultUA,
		SessionSave:          30,
		TunedStorage:         false,
		CheckAvailableSpace:  true,
		ConnectionsLimit:     0,
		LimitAfterBuffering:  false,
		MaxDownloadRate:      0,
		MaxUploadRate:        0,
		ShareRatioLimit:      0,
		SeedTimeRatioLimit:   0,
		SeedTimeLimit:        0,
		ActiveDownloadsLimit: 3,
		ActiveSeedsLimit:     5,
		ActiveCheckingLimit:  1,
		ActiveDhtLimit:       88,
		ActiveTrackerLimit:   1600,
		ActiveLsdLimit:       60,
		ActiveLimit:          500,
		EncryptionPolicy:     EncryptionEnabledPolicy,
		Proxy:                nil,
		BufferSize:           20 * 1024 * 1024,
		PieceWaitTimeout:     60,
		ServiceLogLevel:      logging.INFO,
		AlertsLogLevel:       logging.CRITICAL,
		ApiLogLevel:          logging.ERROR,
	}
}

// Load loads settings from path
func Load(path string) (s *Settings, err error) {
	s = DefaultSettings()
	s.SetSettingsPath(path)

	if data, e := ioutil.ReadFile(path); e == nil {
		err = s.Update(data)
	}

	return s, err
}

// SetSettingsPath sets the path where to save settings
func (s *Settings) SetSettingsPath(path string) {
	s.settingsPath = path
}

// Update updates the settings with the json object provided
func (s *Settings) Update(data []byte) (err error) {
	if err = json.Unmarshal(data, s); err == nil {
		err = validate.Struct(s)
	}
	return
}

// Clone clones the settings
func (s *Settings) Clone() *Settings {
	n := &Settings{settingsPath: s.settingsPath}
	if err := n.UpdateFrom(s); err != nil {
		panic("Failed cloning settings: " + err.Error())
	}
	return n
}

// UpdateFrom updates the settings with the settings object provided
func (s *Settings) UpdateFrom(settings *Settings) error {
	return copier.Copy(s, settings)
}

// Save saves the current settings to path
func (s *Settings) Save() (err error) {
	var data []byte
	if data, err = json.MarshalIndent(s, "", "   "); err == nil {
		err = ioutil.WriteFile(s.settingsPath, data, 0644)
	}
	return
}
