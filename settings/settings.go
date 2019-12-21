package settings

import (
	"encoding/json"
	"io/ioutil"

	"github.com/jinzhu/copier"
)

// Settings define the server settings
type Settings struct {
	DummySetting string `json:"dummy-setting"`
}

// Load loads settings from path
func Load(path string) (s *Settings, err error) {
	s = &Settings{
		DummySetting: "test",
	}

	if data, e := ioutil.ReadFile(path); e == nil {
		err = s.Update(data)
	}

	return s, err
}

// Update updates the settings with the json object provided
func (s *Settings) Update(data []byte) error {
	return json.Unmarshal(data, s)
}

// Clone clones the settings
func (s *Settings) Clone() *Settings {
	n := new(Settings)
	copier.Copy(n, s)
	return n
}

// Save saves the current settings to path
func (s *Settings) Save(path string) (err error) {
	var data []byte
	if data, err = json.MarshalIndent(s, "", "   "); err == nil {
		err = ioutil.WriteFile(path, data, 0644)
	}
	return
}
