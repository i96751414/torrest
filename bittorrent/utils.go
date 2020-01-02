package bittorrent

import (
	"fmt"
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
			panic(fmt.Sprintf("Unable to create %s folder", path))
		}
	}
}

func copyFileContents(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return
	}
	//noinspection GoUnhandledErrorResult
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return
	}
	defer func() {
		if e := out.Close(); err == nil {
			err = e
		}
	}()
	if _, err = io.Copy(out, in); err == nil {
		err = out.Sync()
	}
	return
}
