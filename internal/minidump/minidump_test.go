package minidump

import (
	"encoding/binary"
	"testing"
)

func TestParseValidMinidump(t *testing.T) {
	data := buildTestMinidump(t)
	file, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(file.Modules) != 1 {
		t.Fatalf("module count = %d, want 1", len(file.Modules))
	}
	module := file.Modules[0]
	if module.Name != "app.dylib" {
		t.Fatalf("module.Name = %q, want app.dylib", module.Name)
	}
	if module.BaseOfImage != 0x1000 || module.SizeOfImage != 0x2000 {
		t.Fatalf("module = %+v", module)
	}
	if file.Exception.ThreadID != 77 || file.Exception.Address != 0xfeedface {
		t.Fatalf("exception = %+v", file.Exception)
	}
}

func TestParseRejectsInvalidSignature(t *testing.T) {
	data := buildTestMinidump(t)
	copy(data[:4], []byte("NOPE"))
	if _, err := Parse(data); err == nil {
		t.Fatal("expected invalid signature error")
	}
}

func TestParseRejectsMissingModuleList(t *testing.T) {
	data := buildTestMinidump(t)
	binary.LittleEndian.PutUint32(data[headerSize:headerSize+4], streamTypeException)
	if _, err := Parse(data); err == nil || err.Error() != "minidump module list is missing" {
		t.Fatalf("Parse error = %v, want missing module list", err)
	}
}

func buildTestMinidump(t *testing.T) []byte {
	t.Helper()
	const (
		dirRVA        = headerSize
		moduleListRVA = dirRVA + 2*streamDirectorySize
		exceptionRVA  = moduleListRVA + 4 + moduleRecordSize
		nameRVA       = exceptionRVA + 32
	)

	name := encodeUTF16String("app.dylib")
	data := make([]byte, nameRVA+uint32(len(name)))
	copy(data[:4], []byte("MDMP"))
	binary.LittleEndian.PutUint32(data[8:12], 2)
	binary.LittleEndian.PutUint32(data[12:16], dirRVA)

	putDir := func(index int, streamType, size, rva uint32) {
		offset := dirRVA + uint32(index)*streamDirectorySize
		binary.LittleEndian.PutUint32(data[offset:offset+4], streamType)
		binary.LittleEndian.PutUint32(data[offset+4:offset+8], size)
		binary.LittleEndian.PutUint32(data[offset+8:offset+12], rva)
	}
	putDir(0, streamTypeModuleList, 4+moduleRecordSize, moduleListRVA)
	putDir(1, streamTypeException, 32, exceptionRVA)

	binary.LittleEndian.PutUint32(data[moduleListRVA:moduleListRVA+4], 1)
	record := moduleListRVA + 4
	binary.LittleEndian.PutUint64(data[record:record+8], 0x1000)
	binary.LittleEndian.PutUint32(data[record+8:record+12], 0x2000)
	binary.LittleEndian.PutUint32(data[record+16:record+20], nameRVA)

	binary.LittleEndian.PutUint32(data[exceptionRVA:exceptionRVA+4], 77)
	binary.LittleEndian.PutUint64(data[exceptionRVA+24:exceptionRVA+32], 0xfeedface)

	copy(data[nameRVA:], name)
	return data
}

func encodeUTF16String(value string) []byte {
	encoded := make([]byte, 4+len(value)*2)
	binary.LittleEndian.PutUint32(encoded[:4], uint32(len(value)*2))
	for i, r := range value {
		binary.LittleEndian.PutUint16(encoded[4+i*2:6+i*2], uint16(r))
	}
	return encoded
}
