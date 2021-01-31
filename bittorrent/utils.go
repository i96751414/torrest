package bittorrent

import (
	"encoding/gob"
	"io"
	"os"
)

func deleteFile(path string) {
	if _, err := os.Stat(path); err == nil {
		if err := os.Remove(path); err != nil {
			log.Errorf("Failed to remove file '%s': %s", path, err)
		}
	}
}

func createDir(path string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.Mkdir(path, 0755); err != nil {
			panic("Unable to create '" + path + "' folder: " + err.Error())
		}
	}
}

func copyFileContents(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err == nil {
		err = out.Sync()
	}
	if e := out.Close(); err == nil {
		err = e
	}
	_ = in.Close()
	return err
}

func hasFlagsUint64(flags, f uint64) bool {
	return flags&f == f
}

func saveGobData(path string, data interface{}, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	err = gob.NewEncoder(f).Encode(data)
	if e := f.Close(); err == nil {
		err = e
	}
	return err
}

func readGobData(path string, data interface{}) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	err = gob.NewDecoder(f).Decode(data)
	if e := f.Close(); err == nil {
		err = e
	}
	return err
}

/*
// Functions left for testing
func saveJsonData(path string, data interface{}, perm os.FileMode) error {
	d, err := json.Marshal(data)
	if err == nil {
		err = ioutil.WriteFile(path, d, perm)
	}
	return err
}

func readJsonData(path string, data interface{}) error {
	d, err := ioutil.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(d, data)
	}
	return err
}
*/
