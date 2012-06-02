package vfat

import (
	"bytes"
	"encoding/binary"
	"unicode/utf16"
)

type Attribute uint8

const (
	AttrReadOnly  Attribute = 0x01
	AttrHidden              = 0x02
	AttrSystem              = 0x04
	AttrVolumeID            = 0x08
	AttrDirectory           = 0x10
	AttrArchive             = 0x20
	AttrLongName            = AttrReadOnly | AttrHidden | AttrSystem | AttrVolumeID
)

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

func (file FileRecord) FirstCluster() uint32 {
	// TODO does this also support FAT12 and FAT16?
	return (uint32(file.FstClusHI & 0x0FFF)) | uint32(file.FstClusLO)
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
