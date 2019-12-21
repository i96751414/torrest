package bittorrent

import (
	"github.com/i96751414/libtorrent-go"
	"github.com/i96751414/torrest/broadcast"
)

// Service represents the torrent service
type Service struct {
	Session libtorrent.Session
	//config            *BTConfiguration
	alertsBroadcaster *broadcast.Broadcaster
	settingsPack      libtorrent.SettingsPack
	UserAgent         string
	closing           chan interface{}
}
