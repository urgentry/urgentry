package minidump

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"
	"unicode/utf16"
)

type ModuleSpec struct {
	BaseOfImage uint64
	SizeOfImage uint32
	Name        string
}

func Build(t testing.TB, exceptionAddr, imageAddr uint64, imageSize uint32, moduleName string) []byte {
	t.Helper()
	data, err := BuildBytes(exceptionAddr, imageAddr, imageSize, moduleName)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func BuildBytes(exceptionAddr, imageAddr uint64, imageSize uint32, moduleName string) ([]byte, error) {
	return BuildModulesBytes(exceptionAddr, []ModuleSpec{{
		BaseOfImage: imageAddr,
		SizeOfImage: imageSize,
		Name:        moduleName,
	}})
}

func BuildModules(t testing.TB, exceptionAddr uint64, modules []ModuleSpec) []byte {
	t.Helper()
	data, err := BuildModulesBytes(exceptionAddr, modules)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func BuildModulesBytes(exceptionAddr uint64, modules []ModuleSpec) ([]byte, error) {
	if len(modules) == 0 {
		return nil, fmt.Errorf("minidump fixture requires at least one module")
	}

	const (
		headerSize           = 32
		streamDirectorySize  = 12
		moduleRecordSize     = 108
		moduleListStreamType = 4
		exceptionStreamType  = 6
	)

	moduleListOffset := uint32(headerSize + 2*streamDirectorySize)
	moduleListSize := uint32(4 + len(modules)*moduleRecordSize)
	exceptionOffset := moduleListOffset + moduleListSize
	exceptionSize := uint32(32)

	nameBodies := make([][]byte, 0, len(modules))
	nameBytes := uint32(0)
	for _, module := range modules {
		nameWords := utf16.Encode([]rune(module.Name))
		nameBody := make([]byte, len(nameWords)*2)
		for i, word := range nameWords {
			binary.LittleEndian.PutUint16(nameBody[i*2:], word)
		}
		nameBodies = append(nameBodies, nameBody)
		nameBytes += uint32(4 + len(nameBody))
	}
	nameOffset := exceptionOffset + exceptionSize
	nameSize := nameBytes

	buf := make([]byte, nameOffset+nameSize)
	copy(buf[:4], []byte("MDMP"))
	binary.LittleEndian.PutUint32(buf[4:], 0x0000a793)
	binary.LittleEndian.PutUint32(buf[8:], 2)
	binary.LittleEndian.PutUint32(buf[12:], headerSize)

	writeDir := func(offset, streamType, size, rva uint32) {
		binary.LittleEndian.PutUint32(buf[offset:], streamType)
		binary.LittleEndian.PutUint32(buf[offset+4:], size)
		binary.LittleEndian.PutUint32(buf[offset+8:], rva)
	}
	writeDir(headerSize, moduleListStreamType, moduleListSize, moduleListOffset)
	writeDir(headerSize+streamDirectorySize, exceptionStreamType, exceptionSize, exceptionOffset)

	binary.LittleEndian.PutUint32(buf[moduleListOffset:], uint32(len(modules)))
	moduleOffset := moduleListOffset + 4
	currentNameOffset := nameOffset
	for i, module := range modules {
		recordOffset := moduleOffset + uint32(i)*moduleRecordSize
		binary.LittleEndian.PutUint64(buf[recordOffset:], module.BaseOfImage)
		binary.LittleEndian.PutUint32(buf[recordOffset+8:], module.SizeOfImage)
		binary.LittleEndian.PutUint32(buf[recordOffset+16:], currentNameOffset)

		nameBody := nameBodies[i]
		binary.LittleEndian.PutUint32(buf[currentNameOffset:], uint32(len(nameBody)))
		copy(buf[currentNameOffset+4:], nameBody)
		currentNameOffset += uint32(4 + len(nameBody))
	}

	binary.LittleEndian.PutUint32(buf[exceptionOffset:], 1)
	binary.LittleEndian.PutUint64(buf[exceptionOffset+24:], exceptionAddr)

	if bytes.Equal(buf[:4], []byte("MDMP")) {
		return buf, nil
	}
	return nil, fmt.Errorf("failed to build minidump fixture")
}
