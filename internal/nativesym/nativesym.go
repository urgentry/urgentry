package nativesym

import (
	"bufio"
	"bytes"
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var (
	ErrMalformedSymbolSource   = errors.New("malformed native symbol source")
	ErrUnsupportedSymbolSource = errors.New("unsupported native symbol source")
)

type LookupStatus string

const (
	LookupStatusResolved    LookupStatus = "resolved"
	LookupStatusMiss        LookupStatus = "miss"
	LookupStatusMalformed   LookupStatus = "malformed"
	LookupStatusUnsupported LookupStatus = "unsupported"
)

// File is the minimum metadata the resolver needs from a stored debug file.
type File struct {
	ID     string
	CodeID string
	Kind   string
}

// LookupRequest is the normalized native image record used for symbol lookup.
type LookupRequest struct {
	ProjectID       string
	ReleaseVersion  string
	DebugID         string
	CodeID          string
	BuildID         string
	ModuleName      string
	InstructionAddr string
}

// LookupResult captures one native symbol lookup outcome.
type LookupResult struct {
	Module   string
	File     string
	Function string
	Line     int
	Status   LookupStatus
	Format   string
}

// Store is the debug-file lookup surface required by the resolver.
type Store interface {
	LookupByDebugID(ctx context.Context, projectID, releaseVersion, kind, debugID string) (*File, []byte, error)
	LookupByCodeID(ctx context.Context, projectID, releaseVersion, kind, codeID string) (*File, []byte, error)
}

type parsedFile interface {
	resolve(addr uint64) *resolvedSymbol
	moduleName() string
	format() string
}

type formatResolver interface {
	format() string
	canParse(file *File, body []byte) bool
	parse(file *File, body []byte) (parsedFile, error)
}

// Resolver symbolicates native frames from uploaded symbol files.
type Resolver struct {
	Store Store

	mu      sync.RWMutex
	cache   map[string]parsedFile
	formats []formatResolver
}

// NewResolver creates a native symbol resolver with an internal parse cache.
func NewResolver(store Store) *Resolver {
	return &Resolver{
		Store: store,
		cache: make(map[string]parsedFile),
		formats: []formatResolver{
			breakpadFormatResolver{},
			elfFormatResolver{},
		},
	}
}

// Resolve maps a native frame to the nearest symbol and source location.
func (r *Resolver) Resolve(ctx context.Context, req LookupRequest) (LookupResult, error) {
	if r == nil || r.Store == nil {
		return LookupResult{Status: LookupStatusMiss}, nil
	}
	addr, ok := parseHexAddress(req.InstructionAddr)
	if !ok {
		return LookupResult{Status: LookupStatusMiss}, nil
	}

	meta, body, err := r.lookup(ctx, req)
	if err != nil {
		return LookupResult{Status: LookupStatusMalformed}, err
	}
	if meta == nil || len(body) == 0 {
		return LookupResult{Status: LookupStatusMiss}, nil
	}

	fileData, err := r.load(meta, body)
	if err != nil {
		switch {
		case errors.Is(err, ErrUnsupportedSymbolSource):
			return LookupResult{Status: LookupStatusUnsupported}, err
		default:
			return LookupResult{Status: LookupStatusMalformed}, err
		}
	}

	result := LookupResult{
		Module: fileData.moduleName(),
		Format: fileData.format(),
		Status: LookupStatusMiss,
	}
	match := fileData.resolve(addr)
	if match == nil {
		return result, nil
	}
	result.File = match.file
	result.Function = match.function
	result.Line = match.line
	result.Status = LookupStatusResolved
	return result, nil
}

// SymbolicationStatus classifies whether a stored symbol source is usable.
func (r *Resolver) SymbolicationStatus(file *File, body []byte) LookupStatus {
	if file == nil || len(bytes.TrimSpace(body)) == 0 {
		return LookupStatusMiss
	}
	_, err := r.load(file, body)
	if err == nil {
		return LookupStatusResolved
	}
	if errors.Is(err, ErrUnsupportedSymbolSource) {
		return LookupStatusUnsupported
	}
	return LookupStatusMalformed
}

func (r *Resolver) lookup(ctx context.Context, req LookupRequest) (*File, []byte, error) {
	debug := strings.TrimSpace(req.DebugID)
	code := firstNonEmpty(strings.TrimSpace(req.CodeID), strings.TrimSpace(req.BuildID))

	if debug != "" {
		meta, body, err := r.Store.LookupByDebugID(ctx, req.ProjectID, req.ReleaseVersion, "", debug)
		if err != nil || meta != nil {
			return meta, body, err
		}
	}
	if code != "" {
		meta, body, err := r.Store.LookupByCodeID(ctx, req.ProjectID, req.ReleaseVersion, "", code)
		if err != nil || meta != nil {
			return meta, body, err
		}
	}
	return nil, nil, nil
}

func (r *Resolver) load(file *File, body []byte) (parsedFile, error) {
	if file == nil {
		return nil, fmt.Errorf("%w: missing symbol file metadata", ErrUnsupportedSymbolSource)
	}
	r.mu.RLock()
	cached := r.cache[file.ID]
	r.mu.RUnlock()
	if cached != nil {
		return cached, nil
	}

	for _, candidate := range r.formats {
		if !candidate.canParse(file, body) {
			continue
		}
		parsed, err := candidate.parse(file, body)
		if err != nil {
			return nil, err
		}
		r.mu.Lock()
		if existing := r.cache[file.ID]; existing != nil {
			r.mu.Unlock()
			return existing, nil
		}
		r.cache[file.ID] = parsed
		r.mu.Unlock()
		return parsed, nil
	}
	return nil, fmt.Errorf("%w: kind=%s", ErrUnsupportedSymbolSource, strings.TrimSpace(file.Kind))
}

type breakpadFormatResolver struct{}

func (breakpadFormatResolver) format() string { return "breakpad" }

func (breakpadFormatResolver) canParse(_ *File, body []byte) bool {
	return bytes.HasPrefix(bytes.TrimSpace(body), []byte("MODULE "))
}

func (breakpadFormatResolver) parse(_ *File, body []byte) (parsedFile, error) {
	return parseSymbolFile(body)
}

type elfFormatResolver struct{}

func (elfFormatResolver) format() string { return "elf" }

func (elfFormatResolver) canParse(file *File, body []byte) bool {
	if len(body) >= 4 && bytes.Equal(body[:4], []byte{0x7f, 'E', 'L', 'F'}) {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(file.Kind), "elf")
}

func (elfFormatResolver) parse(file *File, body []byte) (parsedFile, error) {
	parsed, err := parseELFFile(body)
	if err != nil {
		return nil, err
	}
	if parsed.module == "" {
		parsed.module = strings.TrimSpace(file.CodeID)
	}
	return parsed, nil
}

type symbolFile struct {
	module string
	files  map[int]string
	funcs  []symbolFunc
}

func (sf *symbolFile) moduleName() string { return sf.module }
func (sf *symbolFile) format() string     { return "breakpad" }

type binarySymbolFile struct {
	module  string
	symbols []binarySymbol
	kind    string
}

func (sf *binarySymbolFile) moduleName() string { return sf.module }
func (sf *binarySymbolFile) format() string     { return sf.kind }

type binarySymbol struct {
	address uint64
	size    uint64
	name    string
}

type symbolFunc struct {
	address uint64
	size    uint64
	name    string
	lines   []symbolLine
}

type symbolLine struct {
	address uint64
	size    uint64
	line    int
	fileID  int
}

type resolvedSymbol struct {
	file     string
	function string
	line     int
}

func parseSymbolFile(body []byte) (*symbolFile, error) {
	sf := &symbolFile{files: make(map[int]string)}

	var current *symbolFunc
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "MODULE "):
			parts := strings.Fields(line)
			if len(parts) >= 5 {
				sf.module = parts[len(parts)-1]
			}
			current = nil
		case strings.HasPrefix(line, "FILE "):
			parts := strings.SplitN(line, " ", 3)
			if len(parts) != 3 {
				continue
			}
			fileID, err := strconv.Atoi(parts[1])
			if err != nil {
				continue
			}
			sf.files[fileID] = strings.TrimSpace(parts[2])
		case strings.HasPrefix(line, "FUNC "):
			fields := strings.Fields(line)
			if len(fields) < 4 {
				current = nil
				continue
			}
			offset := 1
			if fields[1] == "m" {
				offset++
			}
			if len(fields) <= offset+2 {
				current = nil
				continue
			}
			address, ok := parseHexAddress(fields[offset])
			if !ok {
				current = nil
				continue
			}
			size, ok := parseHexAddress(fields[offset+1])
			if !ok {
				current = nil
				continue
			}
			name := strings.Join(fields[offset+3:], " ")
			sf.funcs = append(sf.funcs, symbolFunc{
				address: address,
				size:    size,
				name:    strings.TrimSpace(name),
			})
			current = &sf.funcs[len(sf.funcs)-1]
		default:
			if current == nil {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 4 {
				continue
			}
			address, ok := parseHexAddress(fields[0])
			if !ok {
				continue
			}
			size, ok := parseHexAddress(fields[1])
			if !ok {
				continue
			}
			lineNo, err := strconv.Atoi(fields[2])
			if err != nil {
				continue
			}
			fileID, err := strconv.Atoi(fields[3])
			if err != nil {
				continue
			}
			current.lines = append(current.lines, symbolLine{
				address: address,
				size:    size,
				line:    lineNo,
				fileID:  fileID,
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: breakpad scan: %w", ErrMalformedSymbolSource, err)
	}
	if sf.module == "" || len(sf.funcs) == 0 {
		return nil, fmt.Errorf("%w: breakpad body is missing module or function data", ErrMalformedSymbolSource)
	}

	sort.Slice(sf.funcs, func(i, j int) bool {
		return sf.funcs[i].address < sf.funcs[j].address
	})
	for i := range sf.funcs {
		sort.Slice(sf.funcs[i].lines, func(a, b int) bool {
			return sf.funcs[i].lines[a].address < sf.funcs[i].lines[b].address
		})
	}
	return sf, nil
}

func parseELFFile(body []byte) (*binarySymbolFile, error) {
	file, err := elf.NewFile(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: elf parse: %w", ErrMalformedSymbolSource, err)
	}

	symbols, err := file.Symbols()
	if err != nil || len(symbols) == 0 {
		symbols, err = file.DynamicSymbols()
	}
	if err != nil {
		return nil, fmt.Errorf("%w: elf symbols: %w", ErrMalformedSymbolSource, err)
	}

	parsed := &binarySymbolFile{kind: "elf"}
	for _, sym := range symbols {
		if strings.TrimSpace(sym.Name) == "" {
			continue
		}
		if elf.ST_TYPE(sym.Info) != elf.STT_FUNC {
			continue
		}
		parsed.symbols = append(parsed.symbols, binarySymbol{
			address: sym.Value,
			size:    sym.Size,
			name:    sym.Name,
		})
	}
	if len(parsed.symbols) == 0 {
		return nil, fmt.Errorf("%w: elf symbol table has no function symbols", ErrMalformedSymbolSource)
	}
	sort.Slice(parsed.symbols, func(i, j int) bool {
		return parsed.symbols[i].address < parsed.symbols[j].address
	})
	for i := range parsed.symbols {
		if parsed.symbols[i].size != 0 || i == len(parsed.symbols)-1 {
			continue
		}
		next := parsed.symbols[i+1].address
		if next > parsed.symbols[i].address {
			parsed.symbols[i].size = next - parsed.symbols[i].address
		}
	}
	return parsed, nil
}

func (sf *symbolFile) resolve(addr uint64) *resolvedSymbol {
	for i := range sf.funcs {
		fn := &sf.funcs[i]
		if !containsAddress(fn.address, fn.size, addr) {
			continue
		}
		result := &resolvedSymbol{function: fn.name}
		for _, line := range fn.lines {
			if !containsAddress(line.address, line.size, addr) {
				continue
			}
			result.line = line.line
			result.file = sf.files[line.fileID]
			return result
		}
		return result
	}
	return nil
}

func (sf *binarySymbolFile) resolve(addr uint64) *resolvedSymbol {
	for i := range sf.symbols {
		sym := sf.symbols[i]
		if !containsAddress(sym.address, sym.size, addr) {
			continue
		}
		return &resolvedSymbol{function: sym.name}
	}
	return nil
}

func containsAddress(start, size, addr uint64) bool {
	if size == 0 {
		return addr == start
	}
	return addr >= start && addr < start+size
}

func parseHexAddress(value string) (uint64, bool) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(strings.ToLower(value), "0x")
	if value == "" {
		return 0, false
	}
	addr, err := strconv.ParseUint(value, 16, 64)
	if err != nil {
		return 0, false
	}
	return addr, true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
