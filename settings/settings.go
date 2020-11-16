package settings

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"time"

	"github.com/jinzhu/copier"
	"github.com/op/go-logging"
	//"github.com/go-playground/validator"
	"gopkg.in/go-playground/validator.v9"
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

	ListenPort          uint             `json:"listen_port" validate:"gte=0,lte=65535" example:"6889"`
	ListenInterfaces    string           `json:"listen_interfaces" example:""`
	OutgoingInterfaces  string           `json:"outgoing_interfaces" example:""`
	DisableDHT          bool             `json:"disable_dht" example:"false"`
	DisableUPNP         bool             `json:"disable_upnp" example:"false"`
	DownloadPath        string           `json:"download_path" validate:"required" example:"downloads"`
	TorrentsPath        string           `json:"torrents_path" validate:"required" example:"downloads/Torrents"`
	UserAgent           UserAgentType    `json:"user_agent" validate:"gte=0,lte=6" example:"0"`
	SessionSave         time.Duration    `json:"session_save" validate:"gt=0" example:"30" swaggertype:"integer"`
	TunedStorage        bool             `json:"tuned_storage" example:"false"`
	ConnectionsLimit    int              `json:"connections_limit" example:"0"`
	LimitAfterBuffering bool             `json:"limit_after_buffering" example:"false"`
	MaxDownloadRate     int              `json:"max_download_rate" example:"0"`
	MaxUploadRate       int              `json:"max_upload_rate" example:"0"`
	ShareRatioLimit     int              `json:"share_ratio_limit" example:"0"`
	SeedTimeRatioLimit  int64            `json:"seed_time_ratio_limit" example:"0"`
	SeedTimeLimit       int64            `json:"seed_time_limit" example:"0"`
	EncryptionPolicy    EncryptionPolicy `json:"encryption_policy" validate:"gte=0,lte=2" example:"0"`
	Proxy               *ProxySettings   `json:"proxy"`
	BufferSize          int64            `json:"buffer_size" example:"20971520"`
	PieceWaitTimeout    time.Duration    `json:"piece_wait_timeout" validate:"gte=0" example:"60" swaggertype:"integer"`
	ServiceLogLevel     logging.Level    `json:"service_log_level" validate:"gte=0,lte=5" example:"4" swaggertype:"integer"`
	AlertsLogLevel      logging.Level    `json:"alerts_log_level" validate:"gte=0,lte=5" example:"0" swaggertype:"integer"`
}

func DefaultSettings() *Settings {
	return &Settings{
		settingsPath:        "settings.json",
		ListenPort:          6889,
		ListenInterfaces:    "",
		OutgoingInterfaces:  "",
		DisableDHT:          false,
		DisableUPNP:         false,
		DownloadPath:        "downloads",
		TorrentsPath:        filepath.Join("downloads", "Torrents"),
		UserAgent:           DefaultUA,
		SessionSave:         30,
		TunedStorage:        false,
		ConnectionsLimit:    0,
		LimitAfterBuffering: false,
		MaxDownloadRate:     0,
		MaxUploadRate:       0,
		ShareRatioLimit:     0,
		SeedTimeRatioLimit:  0,
		SeedTimeLimit:       0,
		EncryptionPolicy:    EncryptionEnabledPolicy,
		Proxy:               nil,
		BufferSize:          20 * 1024 * 1024,
		PieceWaitTimeout:    60,
		ServiceLogLevel:     logging.INFO,
		AlertsLogLevel:      logging.CRITICAL,
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
	settings := s.Clone()
	if err = json.Unmarshal(data, settings); err == nil {
		if err = validate.Struct(settings); err == nil {
			err = copier.Copy(s, settings)
		}
	}
	return
}

// Clone clones the settings
func (s *Settings) Clone() *Settings {
	n := new(Settings)
	if err := copier.Copy(n, s); err != nil {
		panic(fmt.Sprintf("Failed cloning settings: %s", err))
	}
	return n
}

// Save saves the current settings to path
func (s *Settings) Save() (err error) {
	var data []byte
	if data, err = json.MarshalIndent(s, "", "   "); err == nil {
		err = ioutil.WriteFile(s.settingsPath, data, 0644)
	}
	return
}
