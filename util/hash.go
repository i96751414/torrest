package util

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

const chunkSize = 64 * 1024

func readAt(file io.ReadSeeker, buf []byte, off int64) (n int, err error) {
	_, err = file.Seek(off, io.SeekStart)
	if err == nil {
		return file.Read(buf)
	}
	return
}

// Hash file based on https://trac.opensubtitles.org/projects/opensubtitles/wiki/HashSourceCodes
func HashFile(file io.ReadSeeker, size int64) (string, error) {
	if size < chunkSize {
		return "", errors.New("file is too small")
	}

	// Read head and tail blocks.
	buf := make([]byte, chunkSize*2)
	if _, err := readAt(file, buf[:chunkSize], 0); err != nil {
		return "", err
	}
	if _, err := readAt(file, buf[chunkSize:], size-chunkSize); err != nil {
		return "", err
	}

	// Convert to uint64 and sum
	nums := make([]uint64, (chunkSize*2)/8)
	reader := bytes.NewReader(buf)
	if err := binary.Read(reader, binary.LittleEndian, &nums); err != nil {
		return "", err
	}

	var hash uint64
	for _, num := range nums {
		hash += num
	}

	return fmt.Sprintf("%016x", hash+uint64(size)), nil
}

func Hash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	//noinspection GoUnhandledErrorResult
	defer file.Close()
	stats, err := file.Stat()
	if err != nil {
		return "", err
	}
	return HashFile(file, stats.Size())
}
