package vfat

import (
	"encoding/binary"
	"io"
	"strings"
)

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
	FAT12 Type = iota
	FAT16
	FAT32
	UnknownType
)

type FS struct {
	BPB  *BPB32
	Type Type
	Data io.ReadSeeker
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

func (fs FS) RootSector() (rootSector uint32) {
	switch fs.DetermineType() {
	case FAT12, FAT16:
		rootSector = uint32(fs.BPB.ResvdSecCnt + (uint16(fs.BPB.NumFATs) * fs.BPB.FATSz16))
	case FAT32:
		rootSector = fs.FirstSectorOfCluster(fs.BPB.RootClus)
	}

	return
}

func (fs FS) ReadDirectoryFromSector(sector uint32) []File {
	firstByte := sector * uint32(fs.BPB.BytsPerSec)

	fs.Data.Seek(int64(firstByte), 0)

	var curLongName []string
	var curLongNameString string
	files := make([]File, 0)

	for i := 0; i < 20; i++ {
		file := &FileRecord{}
		err := binary.Read(fs.Data, binary.LittleEndian, file)
		if err != nil {
			// TODO return error
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
