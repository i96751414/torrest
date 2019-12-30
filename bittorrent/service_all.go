// +build !arm

package bittorrent

import "github.com/i96751414/libtorrent-go"

// Nothing to do on regular devices
//noinspection GoUnusedParameter
func setPlatformSpecificSettings(_ libtorrent.SettingsPack) {
}
