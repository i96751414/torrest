// +build arm

package bittorrent

import (
	"runtime"

	"github.com/i96751414/libtorrent-go"
)

const (
	maxSingleCoreConnections = 50
)

// On Raspberry Pi, we need to limit the number of active connections
// because otherwise it fries. So here we need to detect that we are on RPi
// (or, rather, a single cpu arm machine, no need to be specific to RPi) and
// set those limits.
func setPlatformSpecificSettings(settingsPack libtorrent.SettingsPack) {
	if runtime.NumCPU() == 1 { // single core?
		log.Debug("Setting max single core connections limit")
		settingsPack.SetInt("connections_limit", maxSingleCoreConnections)
	}
}
