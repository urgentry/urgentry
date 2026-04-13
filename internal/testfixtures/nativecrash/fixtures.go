package nativecrash

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"urgentry/internal/normalize"
	minidumpfixture "urgentry/internal/testfixtures/minidump"
	nativesymfixture "urgentry/internal/testfixtures/nativesym"
)

type Image struct {
	CodeFile  string
	Module    string
	DebugID   string
	CodeID    string
	ImageAddr string
	ImageSize string
	Arch      string
}

type SymbolSource struct {
	Kind    string
	Name    string
	DebugID string
	CodeID  string
	Body    []byte
}

type FrameSnapshot struct {
	InstructionAddr string `json:"instruction_addr,omitempty"`
	Module          string `json:"module,omitempty"`
	Package         string `json:"package,omitempty"`
	DebugID         string `json:"debug_id,omitempty"`
	Function        string `json:"function,omitempty"`
	Filename        string `json:"filename,omitempty"`
	Lineno          int    `json:"lineno,omitempty"`
}

type Fixture struct {
	Name               string
	Release            string
	Platform           string
	DumpFilename       string
	Dump               []byte
	Images             []Image
	Symbols            []SymbolSource
	GoldenFrames       string
	WantResolvedFrames int
	WantUnresolved     int
}

func Corpus(t testing.TB) []Fixture {
	t.Helper()
	return []Fixture{
		ByName(t, "apple_multimodule"),
		ByName(t, "linux_elf"),
		ByName(t, "fallback_module_only"),
	}
}

func CorpusForLibrary() ([]Fixture, error) {
	names := []string{"apple_multimodule", "linux_elf", "fallback_module_only"}
	out := make([]Fixture, 0, len(names))
	for _, name := range names {
		item, err := ByNameForLibrary(name)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func ByName(t testing.TB, name string) Fixture {
	t.Helper()
	switch name {
	case "apple_multimodule":
		return Fixture{
			Name:         name,
			Release:      "ios@1.2.3",
			Platform:     "cocoa",
			DumpFilename: "apple-multimodule.dmp",
			Dump: minidumpfixture.BuildModules(t, 0x1010, []minidumpfixture.ModuleSpec{
				{BaseOfImage: 0x1000, SizeOfImage: 0x200, Name: "App"},
				{BaseOfImage: 0x2000, SizeOfImage: 0x400, Name: "UIKit"},
			}),
			Images: []Image{
				{
					CodeFile:  "App",
					Module:    "App",
					DebugID:   "DEBUG-APPLE-1",
					CodeID:    "CODE-APPLE-1",
					ImageAddr: "0x1000",
					ImageSize: "0x200",
					Arch:      "arm64",
				},
				{
					CodeFile:  "UIKit",
					Module:    "UIKit",
					DebugID:   "DEBUG-APPLE-2",
					CodeID:    "CODE-APPLE-2",
					ImageAddr: "0x2000",
					ImageSize: "0x400",
					Arch:      "arm64",
				},
			},
			Symbols: []SymbolSource{{
				Kind:    "macho",
				Name:    "App.sym",
				DebugID: "DEBUG-APPLE-1",
				CodeID:  "CODE-APPLE-1",
				Body: []byte("MODULE mac arm64 DEBUG-APPLE-1 App\n" +
					"FILE 0 src/AppDelegate.swift\n" +
					"FUNC 1010 10 0 main\n" +
					"1010 10 42 0\n"),
			}},
			GoldenFrames:       "apple_multimodule.frames.json",
			WantResolvedFrames: 1,
			WantUnresolved:     1,
		}
	case "linux_elf":
		return Fixture{
			Name:         name,
			Release:      "linux@1.2.3",
			Platform:     "native",
			DumpFilename: "linux-elf.dmp",
			Dump: minidumpfixture.BuildModules(t, 0x1, []minidumpfixture.ModuleSpec{
				{BaseOfImage: 0x1, SizeOfImage: 0x20, Name: "server"},
			}),
			Images: []Image{{
				CodeFile:  "server",
				Module:    "server",
				CodeID:    "ELF-CODE-1",
				ImageAddr: "0x1",
				ImageSize: "0x20",
				Arch:      "x86_64",
			}},
			Symbols: []SymbolSource{{
				Kind:   "elf",
				Name:   "server.debug",
				CodeID: "ELF-CODE-1",
				Body:   nativesymfixture.ELFHandleRequestObject(t),
			}},
			GoldenFrames:       "linux_elf.frames.json",
			WantResolvedFrames: 1,
			WantUnresolved:     0,
		}
	case "fallback_module_only":
		return Fixture{
			Name:         name,
			Release:      "ios@9.9.9",
			Platform:     "cocoa",
			DumpFilename: "fallback-module-only.dmp",
			Dump: minidumpfixture.BuildModules(t, 0x3010, []minidumpfixture.ModuleSpec{
				{BaseOfImage: 0x3000, SizeOfImage: 0x200, Name: "FallbackApp"},
				{BaseOfImage: 0x4000, SizeOfImage: 0x200, Name: "libsystem"},
			}),
			GoldenFrames:       "fallback_module_only.frames.json",
			WantResolvedFrames: 0,
			WantUnresolved:     2,
		}
	default:
		t.Fatalf("unknown native fixture %q", name)
		return Fixture{}
	}
}

func ByNameForLibrary(name string) (Fixture, error) {
	switch name {
	case "apple_multimodule":
		dump, err := minidumpfixture.BuildModulesBytes(0x1010, []minidumpfixture.ModuleSpec{
			{BaseOfImage: 0x1000, SizeOfImage: 0x200, Name: "App"},
			{BaseOfImage: 0x2000, SizeOfImage: 0x400, Name: "UIKit"},
		})
		if err != nil {
			return Fixture{}, err
		}
		return Fixture{
			Name:         name,
			Release:      "ios@1.2.3",
			Platform:     "cocoa",
			DumpFilename: "apple-multimodule.dmp",
			Dump:         dump,
			Images: []Image{
				{CodeFile: "App", Module: "App", DebugID: "DEBUG-APPLE-1", CodeID: "CODE-APPLE-1", ImageAddr: "0x1000", ImageSize: "0x200", Arch: "arm64"},
				{CodeFile: "UIKit", Module: "UIKit", DebugID: "DEBUG-APPLE-2", CodeID: "CODE-APPLE-2", ImageAddr: "0x2000", ImageSize: "0x400", Arch: "arm64"},
			},
			Symbols: []SymbolSource{{
				Kind:    "macho",
				Name:    "App.sym",
				DebugID: "DEBUG-APPLE-1",
				CodeID:  "CODE-APPLE-1",
				Body: []byte("MODULE mac arm64 DEBUG-APPLE-1 App\n" +
					"FILE 0 src/AppDelegate.swift\n" +
					"FUNC 1010 10 0 main\n" +
					"1010 10 42 0\n"),
			}},
			GoldenFrames:       "apple_multimodule.frames.json",
			WantResolvedFrames: 1,
			WantUnresolved:     1,
		}, nil
	case "linux_elf":
		dump, err := minidumpfixture.BuildModulesBytes(0x1, []minidumpfixture.ModuleSpec{
			{BaseOfImage: 0x1, SizeOfImage: 0x20, Name: "server"},
		})
		if err != nil {
			return Fixture{}, err
		}
		return Fixture{
			Name:         name,
			Release:      "linux@1.2.3",
			Platform:     "native",
			DumpFilename: "linux-elf.dmp",
			Dump:         dump,
			Images:       []Image{{CodeFile: "server", Module: "server", CodeID: "ELF-CODE-1", ImageAddr: "0x1", ImageSize: "0x20", Arch: "x86_64"}},
			Symbols: []SymbolSource{{
				Kind:   "elf",
				Name:   "server.debug",
				CodeID: "ELF-CODE-1",
				Body:   nativesymfixture.ELFHandleRequestObjectBytes(),
			}},
			GoldenFrames:       "linux_elf.frames.json",
			WantResolvedFrames: 1,
			WantUnresolved:     0,
		}, nil
	case "fallback_module_only":
		dump, err := minidumpfixture.BuildModulesBytes(0x3010, []minidumpfixture.ModuleSpec{
			{BaseOfImage: 0x3000, SizeOfImage: 0x200, Name: "FallbackApp"},
			{BaseOfImage: 0x4000, SizeOfImage: 0x200, Name: "libsystem"},
		})
		if err != nil {
			return Fixture{}, err
		}
		return Fixture{
			Name:               name,
			Release:            "ios@9.9.9",
			Platform:           "cocoa",
			DumpFilename:       "fallback-module-only.dmp",
			Dump:               dump,
			GoldenFrames:       "fallback_module_only.frames.json",
			WantResolvedFrames: 0,
			WantUnresolved:     2,
		}, nil
	default:
		return Fixture{}, fmt.Errorf("unknown native fixture %q", name)
	}
}

func (f Fixture) EventJSON(t testing.TB, eventID string) []byte {
	t.Helper()
	body, err := f.EventJSONForLibrary(eventID)
	if err != nil {
		t.Fatalf("marshal native fixture payload: %v", err)
	}
	return body
}

func (f Fixture) EventJSONForLibrary(eventID string) ([]byte, error) {
	payload := map[string]any{
		"event_id": eventID,
		"release":  f.Release,
		"platform": f.Platform,
		"level":    "fatal",
		"message":  "Native crash",
		"tags": map[string]string{
			"ingest.kind": "minidump",
		},
	}
	if len(f.Images) > 0 {
		images := make([]map[string]string, 0, len(f.Images))
		for _, image := range f.Images {
			item := map[string]string{}
			if image.CodeFile != "" {
				item["code_file"] = image.CodeFile
			}
			if image.Module != "" {
				item["module"] = image.Module
			}
			if image.DebugID != "" {
				item["debug_id"] = image.DebugID
			}
			if image.CodeID != "" {
				item["code_id"] = image.CodeID
			}
			if image.ImageAddr != "" {
				item["image_addr"] = image.ImageAddr
			}
			if image.ImageSize != "" {
				item["image_size"] = image.ImageSize
			}
			if image.Arch != "" {
				item["arch"] = image.Arch
			}
			images = append(images, item)
		}
		payload["debug_meta"] = map[string]any{"images": images}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func LoadGoldenFrames(t testing.TB, name string) []FrameSnapshot {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testdataDir(t), name))
	if err != nil {
		t.Fatalf("read native golden %s: %v", name, err)
	}
	var frames []FrameSnapshot
	if err := json.Unmarshal(data, &frames); err != nil {
		t.Fatalf("unmarshal native golden %s: %v", name, err)
	}
	return frames
}

func SnapshotFrames(t testing.TB, rawJSON []byte) []FrameSnapshot {
	t.Helper()
	var evt normalize.Event
	if err := json.Unmarshal(rawJSON, &evt); err != nil {
		t.Fatalf("unmarshal native event snapshot: %v", err)
	}
	if evt.Exception == nil {
		return nil
	}
	frames := make([]FrameSnapshot, 0, 4)
	for _, exc := range evt.Exception.Values {
		if exc.Stacktrace == nil {
			continue
		}
		for _, frame := range exc.Stacktrace.Frames {
			frames = append(frames, FrameSnapshot{
				InstructionAddr: frame.InstructionAddr,
				Module:          frame.Module,
				Package:         frame.Package,
				DebugID:         frame.DebugID,
				Function:        frame.Function,
				Filename:        frame.Filename,
				Lineno:          frame.Lineno,
			})
		}
	}
	return frames
}

func MarshalFrames(t testing.TB, frames []FrameSnapshot) []byte {
	t.Helper()
	data, err := json.MarshalIndent(frames, "", "  ")
	if err != nil {
		t.Fatalf("marshal native frame snapshot: %v", err)
	}
	return data
}

func testdataDir(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "testdata")
}
