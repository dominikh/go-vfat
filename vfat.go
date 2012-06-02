// little endian

// 4 regions: reserved, FAT, root directory (non-existent on fat32),
// file/directory data

// package fat32 // TODO probably turn this into a general fat12/16/32 library
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf16"
)

type Attribute uint8
type Type uint8
type ClusterStatus int

const (
	EOC ClusterStatus = iota
	ReservedCluster
	UnusedCluster
	BadCluster
	NextCluster
)

const (
	eocFAT32 = 0x0FFFFFF8
	eocFAT16 = 0xFFF8
	eocFAT12 = eocFAT16
)

const (
	fat12ClusterCount = 4085
	fat16ClusterCount = 65525
)

const (
	AttrReadOnly  Attribute = 0x01
	AttrHidden              = 0x02
	AttrSystem              = 0x04
	AttrVolumeID            = 0x08
	AttrDirectory           = 0x10
	AttrArchive             = 0x20
	AttrLongName            = AttrReadOnly | AttrHidden | AttrSystem | AttrVolumeID
)

const (
	FAT12 Type = iota
	FAT16
	FAT32
	UnknownType
)

type BPBBase struct {
	JmpBoot     [3]byte
	OEMName     [8]byte
	BytsPerSec  uint16 // one of 512, 1024, 2048, 4096
	SecPerClus  uint8  // must be power of 2 > 0
	ResvdSecCnt uint16 // must not be 0; should be 1 for fat12/fat16
	NumFATs     uint8  // should be 2
	RootEntCnt  uint16
	TotSec16    uint16 // must be 0 for fat32
	Media       uint8  // maybe use constants here
	FATSz16     uint16 // must be 0 for fat32
	SecPerTrk   uint16
	NumHeads    uint16
	HiddSec     uint32
	TotSec32    uint32
}

type BPB12 BPB16

type BPB16Base struct {
	DrvNum     uint8
	Reserved1  uint8
	BootSig    uint8
	VolID      [4]byte
	VolLab     [11]byte
	FilSysType [8]byte // maybe use constants here
}

type BPB16 struct {
	BPBBase
	BPB16Base
}

type BPB32Base struct {
	FATSz32   uint32
	ExtFlags  uint16
	FSVer     uint16
	RootClus  uint32
	FSInfo    uint16
	BkBootSec uint16
	Reserved  [12]byte // Set this to zeros when formatting
}

type BPB32 struct {
	BPBBase
	BPB32Base
	BPB16Base
}

type FS struct {
	BPB  *BPB32
	Type Type
	Data io.ReadSeeker
}

type FileRecord struct {
	// 32 bytes in total
	Name         [11]byte
	Attr         Attribute
	NTRes        uint8
	CrtTimeTenth uint8
	Padding      [6]byte
	FstClusHI    uint16
	WrtTime      uint16
	WrtDate      uint16
	FstClusLO    uint16
	FileSize     uint32
}

func (fs FS) FATSectorCount() uint32 {
	// Taken straight from the specification
	// Works for 12(?), 16 and 32

	if fs.BPB.FATSz16 != 0 {
		return uint32(fs.BPB.FATSz16)
	}

	return fs.BPB.FATSz32
}

func (fs FS) RootDirSectorCount() (sectors uint32) {
	// Taken straight from the specification
	// Works for 12, 16 and 32
	// TODO check if it's be faster to just always do the math operations

	// Microsoft claims that this should be rounded up, but that produces off by ones…
	switch fs.Type {
	case FAT12, FAT16:
		sectors = uint32(((fs.BPB.RootEntCnt * 32) + (fs.BPB.BytsPerSec - 1)) / (fs.BPB.BytsPerSec))
	case FAT32:
		sectors = 0
	}

	return
}

func (fs FS) FirstDataSector() uint32 {
	// Taken straight from the specification
	// Works for 12, 16 and 32
	return uint32(fs.BPB.ResvdSecCnt) + (uint32(fs.BPB.NumFATs) * fs.FATSectorCount()) + fs.RootDirSectorCount()
}

func (fs FS) TotalSectorCount() uint32 {
	// Works for 12(?), 16 and 32
	if fs.BPB.TotSec16 != 0 {
		return uint32(fs.BPB.TotSec16)
	}

	return fs.BPB.TotSec32
}

func (fs FS) DataSectorCount() uint32 {
	// Works for 12(?), 16 and 32
	return fs.TotalSectorCount() - (uint32(fs.BPB.ResvdSecCnt) + (uint32(fs.BPB.NumFATs) * fs.FATSectorCount()) + fs.RootDirSectorCount())
}

func (fs FS) FirstSectorOfCluster(cluster uint32) uint32 {
	// Taken straight from the specification
	// Works for 12, 16 and 32

	return ((cluster - 2) * uint32(fs.BPB.SecPerClus)) + fs.FirstDataSector()
}

func (fs FS) ClusterToFATEntry(cluster uint32) (uint32, uint32) {
	// Works for 16 and 32
	var fatOffset uint32

	switch fs.Type {
	case FAT12:
		fatOffset = cluster + (cluster / 2)
	case FAT16:
		fatOffset = cluster * 2
	case FAT32:
		fatOffset = cluster * 4
	}

	thisFATSecNum := uint32(fs.BPB.ResvdSecCnt) + (fatOffset / uint32(fs.BPB.BytsPerSec))
	thisFATEntOffset := fatOffset % uint32(fs.BPB.BytsPerSec)

	return thisFATSecNum, thisFATEntOffset
}

func (fs FS) ClusterCount() uint32 {
	// Works for 12, 16 and 32
	// round down
	return fs.DataSectorCount() / uint32(fs.BPB.SecPerClus)
}

func (fs FS) DetermineType() Type {
	// Taken straight from the specification
	// Works for 12, 16 and 32
	if fs.ClusterCount() < fat12ClusterCount {
		return FAT12
	} else if fs.ClusterCount() < fat16ClusterCount {
		return FAT16
	}

	return FAT32
}

func NewFS(r io.ReadSeeker) *FS {
	bpb32 := &BPB32{}
	err := binary.Read(r, binary.LittleEndian, bpb32)
	if err != nil {
		// TODO error handling
	}

	fs := &FS{bpb32, UnknownType, r}
	t := fs.DetermineType()
	switch t {
	case FAT32:
		fs.Type = FAT32
		return fs
	case FAT12, FAT16:
		// reread the BPB, this time for the correct fs type
		bpb16 := &BPB16{}
		r.Seek(0, 0)
		err := binary.Read(r, binary.LittleEndian, bpb16)
		if err != nil {
			// TODO error handling
		}
		bpb32 = &BPB32{bpb16.BPBBase, BPB32Base{0, 0, 0, 0, 0, 0, [12]byte{}}, bpb16.BPB16Base}
		fs = &FS{bpb32, t, r}
	}

	return fs
}

func (file FileRecord) ProperName() string {
	// Works for 12, 16 and 32
	s := &bytes.Buffer{}

	if file.Name[0] == 0x05 {
		s.WriteByte(0xE5)
	} else {
		s.WriteByte(file.Name[0])
	}

	s.Write(file.Name[1:])

	return s.String()
}

func (file FileRecord) IsDirectory() bool {
	return (file.Attr & AttrDirectory) > 0
}

func (file FileRecord) IsLongName() bool {
	return (file.Attr & AttrLongName) > 0
}

func (file FileRecord) ToLongName() *LongName {
	buf := &bytes.Buffer{}
	err := binary.Write(buf, binary.LittleEndian, file)
	if err != nil {
		// TODO error checking
	}

	ln := &LongName{}
	err = binary.Read(buf, binary.LittleEndian, ln)

	if err != nil {
		// TODO error checking
	}

	return ln
}

func (file FileRecord) IsUnused() bool {
	return file.Name[0] == 0xE5
}

func (file FileRecord) IsEOD() bool {
	return file.Name[0] == 0
}

type LongName struct {
	Sequence uint8
	Part1    [5]uint16
	Attr     Attribute
	Reserved byte
	Checksum byte
	Part2    [6]uint16
	FstClus  uint16
	Part3    [2]uint16
}

func (ln LongName) IsLast() bool {
	return (ln.Sequence & 0x40) > 0
}

func (ln LongName) String() string {
	s := make([]uint16, 0, 13)

	s = append(s, ln.Part1[:]...)
	s = append(s, ln.Part2[:]...)
	s = append(s, ln.Part3[:]...)

	return string(utf16.Decode(s))
}

type File struct {
	ShortName string
	LongName  string
	*FileRecord
	fs FS
}

func (file FileRecord) FirstCluster() uint32 {
	// TODO does this also support FAT12 and FAT16?
	return (uint32(file.FstClusHI & 0x0FFF)) | uint32(file.FstClusLO)
}

func main() {
	r, _ := os.Open(os.Args[1])
	fs := NewFS(r)

	var rootSector uint32
	switch fs.DetermineType() {
	case FAT12, FAT16:
		rootSector = uint32(fs.BPB.ResvdSecCnt + (uint16(fs.BPB.NumFATs) * fs.BPB.FATSz16))
	case FAT32:
		rootSector = fs.FirstSectorOfCluster(fs.BPB.RootClus)
	}

	files := fs.readDirectoryFromSector(rootSector)

	listFiles(fs, files)
}

func listFiles(fs *FS, files []File) {
	for _, file := range files {
		fmt.Println("Name:", file.LongName)
		fmt.Println("Size:", file.FileSize)
		fmt.Println("Data:", string(file.Read()))
		fmt.Println("Directory:", file.IsDirectory())
		if file.IsDirectory() {
			subfiles, _ := file.Files()
			listFiles(fs, subfiles[2:])
		}
	}
}

func (fs FS) readDirectoryFromSector(sector uint32) []File {
	firstByte := sector * uint32(fs.BPB.BytsPerSec)

	fs.Data.Seek(int64(firstByte), 0)

	var curLongName []string
	var curLongNameString string
	files := make([]File, 0)

	for i := 0; i < 20; i++ {
		file := &FileRecord{}
		err := binary.Read(fs.Data, binary.LittleEndian, file)
		if err != nil {
			fmt.Println("Error:", err)
		}

		if file.IsUnused() {
			continue
		}

		if file.IsEOD() {
			break
		}

		if file.IsLongName() {
			ln := file.ToLongName()
			if ln.IsLast() {
				curLongName = make([]string, 0, 20)
			}

			curLongName = append(curLongName, ln.String())

			if ln.Sequence == 1 || (ln.Sequence|0x40 == 0x41) {
				for i, j := 0, len(curLongName)-1; i < j; i, j = i+1, j-1 {
					curLongName[i], curLongName[j] = curLongName[j], curLongName[i]
				}
				curLongNameString = strings.Split(string(strings.Join(curLongName, "")), "\x00\uFFFF")[0]
			}
		} else {
			newFile := File{"", "", file, fs}
			if curLongNameString != "" {
				newFile.LongName = curLongNameString
				curLongNameString = ""
			}

			newFile.ShortName = file.ProperName()

			files = append(files, newFile)
		}
	}

	return files
}

// TODO think of a clever handling of . and .. entries (which, btw, do
// not exist in the root directory)
func (file File) Files() ([]File, error) {
	if !file.IsDirectory() {
		return nil, errors.New("not a directory")
	}

	return file.fs.readDirectoryFromSector(file.fs.FirstSectorOfCluster(file.FirstCluster())), nil
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

func (fs FS) ClusterStatus(cluster uint32) ClusterStatus {
	if cluster == 0 {
		return UnusedCluster
	}

	if cluster == 1 {
		return ReservedCluster
	}

	switch fs.DetermineType() {
	case FAT12:
		switch cluster {
		case 0xFF6:
			return ReservedCluster
		case 0xFF7:
			return BadCluster
		default:
			if cluster >= 0xFF8 {
				return EOC
			}
		}
	case FAT16:
		switch cluster {
		case 0xFFF6:
			return ReservedCluster
		case 0xFFF7:
			return BadCluster
		default:
			if cluster >= 0xFFF8 {
				return EOC
			}
		}
	case FAT32:
		switch cluster {
		case 0x0FFFFF6:
			return ReservedCluster
		case 0x0FFFFF7:
			return BadCluster
		default:
			if cluster >= 0x0FFFFF8 {
				return EOC
			}
		}
	}

	return NextCluster
}

func (fs FS) ReadFAT(cluster uint32) (newCluster uint32, status ClusterStatus) {
	secFAT, offsetFAT := fs.ClusterToFATEntry(cluster)
	byteFATStart := secFAT * uint32(fs.BPB.BytsPerSec)
	fs.Data.Seek(int64(byteFATStart+offsetFAT), 0)

	t := fs.DetermineType()
	if t == FAT12 {
		var fat uint16
		binary.Read(fs.Data, binary.LittleEndian, &fat)
		if cluster%2 == 0 {
			fat &= 0x0FFF
		} else {
			fat >>= 4
		}

		newCluster = uint32(fat)
	} else if t == FAT16 {
		var fat uint16
		binary.Read(fs.Data, binary.LittleEndian, &fat)

		newCluster = uint32(fat)
	} else {
		var fat uint32
		binary.Read(fs.Data, binary.LittleEndian, &fat)
		fat &= 0x0FFFFFFF

		newCluster = fat
	}

	status = fs.ClusterStatus(newCluster)

	return
}