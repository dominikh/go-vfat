package vfat

import (
	"bytes"
	"errors"
	"io"
)

type File struct {
	ShortName string
	LongName  string
	*FileRecord
	fs FS
}

// TODO think of a clever handling of . and .. entries (which, btw, do
// not exist in the root directory)
func (file File) Files() ([]File, error) {
	if !file.IsDirectory() {
		return nil, errors.New("not a directory")
	}

	return file.fs.ReadDirectoryFromSector(file.fs.FirstSectorOfCluster(file.FirstCluster())), nil
}

func (file File) Read() []byte {
	ret := bytes.Buffer{}
	fs := file.fs
	// Technically, the max cluster size could be 4096 * 128 (19 bit),
	// but according to the specification, 1024 * 64 (16 bit) is the
	// maximum that is expected to work. On the other hand,
	// file.Record.FileSize is 32 bit, so using that for the readSize
	// to avoid casts.
	readSize := uint32(fs.BPB.BytsPerSec) * uint32(fs.BPB.SecPerClus)
	if readSize > file.FileSize {
		readSize = file.FileSize
	}

	buf := make([]byte, readSize)
	cluster := file.FirstCluster()
	readTotal := uint32(0)
	for {
		byteStart := fs.FirstSectorOfCluster(cluster) * uint32(fs.BPB.BytsPerSec)
		fs.Data.Seek(int64(byteStart), 0)
		// TODO check error

		toRead := readSize
		if toRead > (file.FileSize - readTotal) {
			toRead = file.FileSize - readTotal
		}

		read, _ := io.ReadAtLeast(fs.Data, buf, int(toRead))
		readTotal += uint32(read)
		ret.Write(buf[0:toRead])

		nextCluster, status := fs.ReadFAT(cluster)
		if status == EOC {
			break
		}

		cluster = nextCluster
	}

	return ret.Bytes()
}
