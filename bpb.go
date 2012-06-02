package vfat

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
