package minidump

import (
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"
)

const (
	headerSize           = 32
	streamDirectorySize  = 12
	streamTypeModuleList = 4
	streamTypeException  = 6
	moduleRecordSize     = 108
)

type File struct {
	Modules   []Module
	Exception Exception
}

type Module struct {
	BaseOfImage uint64
	SizeOfImage uint32
	Name        string
}

type Exception struct {
	ThreadID uint32
	Address  uint64
}

func Parse(data []byte) (*File, error) {
	if len(data) < headerSize {
		return nil, fmt.Errorf("minidump header is truncated")
	}
	if string(data[:4]) != "MDMP" {
		return nil, fmt.Errorf("invalid minidump signature")
	}
	numStreams := readUint32(data, 8)
	dirRVA := readUint32(data, 12)
	if dirRVA == 0 {
		return nil, fmt.Errorf("minidump stream directory is missing")
	}
	if err := requireBounds(data, dirRVA, numStreams*streamDirectorySize); err != nil {
		return nil, fmt.Errorf("minidump stream directory: %w", err)
	}

	file := &File{}
	for i := uint32(0); i < numStreams; i++ {
		offset := dirRVA + i*streamDirectorySize
		streamType := readUint32(data, offset)
		size := readUint32(data, offset+4)
		rva := readUint32(data, offset+8)
		if size > 0 {
			if err := requireBounds(data, rva, size); err != nil {
				return nil, fmt.Errorf("minidump stream %d: %w", streamType, err)
			}
		}
		switch streamType {
		case streamTypeModuleList:
			modules, err := parseModuleList(data, rva, size)
			if err != nil {
				return nil, err
			}
			file.Modules = modules
		case streamTypeException:
			exc, err := parseExceptionStream(data, rva, size)
			if err != nil {
				return nil, err
			}
			file.Exception = exc
		}
	}

	if len(file.Modules) == 0 {
		return nil, fmt.Errorf("minidump module list is missing")
	}
	if file.Exception.Address == 0 {
		return nil, fmt.Errorf("minidump exception address is missing")
	}
	return file, nil
}

func parseModuleList(data []byte, rva, size uint32) ([]Module, error) {
	if size < 4 {
		return nil, fmt.Errorf("minidump module list is truncated")
	}
	count := readUint32(data, rva)
	if count == 0 {
		return nil, nil
	}
	recordsOffset := rva + 4
	required := 4 + count*moduleRecordSize
	if size < required {
		return nil, fmt.Errorf("minidump module list is truncated")
	}

	modules := make([]Module, 0, count)
	for i := uint32(0); i < count; i++ {
		offset := recordsOffset + i*moduleRecordSize
		name, err := parseUTF16String(data, readUint32(data, offset+16))
		if err != nil {
			return nil, err
		}
		modules = append(modules, Module{
			BaseOfImage: readUint64(data, offset),
			SizeOfImage: readUint32(data, offset+8),
			Name:        strings.TrimSpace(name),
		})
	}
	return modules, nil
}

func parseExceptionStream(data []byte, rva, size uint32) (Exception, error) {
	if size < 24 {
		return Exception{}, fmt.Errorf("minidump exception stream is truncated")
	}
	return Exception{
		ThreadID: readUint32(data, rva),
		Address:  readUint64(data, rva+24),
	}, nil
}

func parseUTF16String(data []byte, rva uint32) (string, error) {
	if err := requireBounds(data, rva, 4); err != nil {
		return "", fmt.Errorf("minidump string length: %w", err)
	}
	byteLen := readUint32(data, rva)
	if byteLen == 0 {
		return "", nil
	}
	if byteLen%2 != 0 {
		return "", fmt.Errorf("minidump string has invalid utf16 byte length")
	}
	if err := requireBounds(data, rva+4, byteLen); err != nil {
		return "", fmt.Errorf("minidump string body: %w", err)
	}
	raw := data[rva+4 : rva+4+byteLen]
	words := make([]uint16, 0, len(raw)/2)
	for i := 0; i < len(raw); i += 2 {
		words = append(words, binary.LittleEndian.Uint16(raw[i:i+2]))
	}
	return string(utf16.Decode(words)), nil
}

func requireBounds(data []byte, rva, size uint32) error {
	end := uint64(rva) + uint64(size)
	if end > uint64(len(data)) {
		return fmt.Errorf("rva %d size %d out of bounds", rva, size)
	}
	return nil
}

func readUint32(data []byte, offset uint32) uint32 {
	return binary.LittleEndian.Uint32(data[offset : offset+4])
}

func readUint64(data []byte, offset uint32) uint64 {
	return binary.LittleEndian.Uint64(data[offset : offset+8])
}
