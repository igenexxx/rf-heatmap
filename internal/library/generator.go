package library

import (
	"fmt"
	"strconv"
	"strings"

	"rf-heatmap/internal/subfile"
)

const (
	// SourceGenerated identifies virtual database-free key-generator catalogs.
	SourceGenerated SourceKind = "generated"

	// PackIDGenCAME12_433 is the built-in 12-bit CAME generator on 433.92 MHz (4096 keys).
	PackIDGenCAME12_433 = "gen:came12-433"
	// PackIDGenLinear10_315 is the built-in 10-bit Linear generator on 315.00 MHz (1024 keys).
	PackIDGenLinear10_315 = "gen:linear10-315"
	// PackIDGenCAME12_868 is the built-in 12-bit CAME generator on 868.35 MHz (4096 keys).
	PackIDGenCAME12_868 = "gen:came12-868"

	defaultGenPreset = "FuriHalSubGhzPresetOok650Async"
)

// GeneratorFreqPresets defines standard ISM carrier frequencies cycled with B in PROBE.
var GeneratorFreqPresets = []uint32{
	315_000_000,
	433_920_000,
	868_350_000,
	915_000_000,
}

// Catalog provides O(1) indexed access to signal entries (disk-backed or generated on the fly).
type Catalog interface {
	Len() int
	EntryAt(idx int) (EntryRef, bool)
}

// SliceCatalog adapts an in-memory []EntryRef slice (User Library or external DB) to Catalog.
type SliceCatalog []EntryRef

// Len returns the number of entries in the slice catalog.
func (sc SliceCatalog) Len() int { return len(sc) }

// EntryAt returns the entry at idx, or false if out of range.
func (sc SliceCatalog) EntryAt(idx int) (EntryRef, bool) {
	if idx < 0 || idx >= len(sc) {
		return EntryRef{}, false
	}
	return sc[idx], true
}

// GeneratorCatalog synthesizes fixed-code Sub-GHz .sub entries on the fly in O(1) memory.
type GeneratorCatalog struct {
	Pack      PackInfo
	Protocol  string
	Bits      int
	TE        int
	FreqHz    uint32
	Preset    string
	Prefix    string
	HexDigits int
	Count     int
}

type generatorSpec struct {
	id        string
	name      string
	protocol  string
	bits      int
	te        int
	freqHz    uint32
	prefix    string
	hexDigits int
	count     int
}

var builtInGenerators = []generatorSpec{
	{
		id:        PackIDGenCAME12_433,
		name:      "CAME-12",
		protocol:  "CAME",
		bits:      12,
		te:        320,
		freqHz:    433_920_000,
		prefix:    "came12",
		hexDigits: 3,
		count:     1 << 12,
	},
	{
		id:        PackIDGenLinear10_315,
		name:      "Linear-10",
		protocol:  "Linear",
		bits:      10,
		te:        500,
		freqHz:    315_000_000,
		prefix:    "linear10",
		hexDigits: 3,
		count:     1 << 10,
	},
	{
		id:        PackIDGenCAME12_868,
		name:      "CAME-868",
		protocol:  "CAME",
		bits:      12,
		te:        320,
		freqHz:    868_350_000,
		prefix:    "came12_868",
		hexDigits: 3,
		count:     1 << 12,
	},
}

// BuiltInGeneratorPacks returns PackInfo metadata for all built-in virtual generator catalogs.
func BuiltInGeneratorPacks() []PackInfo {
	out := make([]PackInfo, 0, len(builtInGenerators))
	for _, g := range builtInGenerators {
		out = append(out, PackInfo{
			ID:       g.id,
			Name:     g.name,
			Source:   SourceGenerated,
			ReadOnly: true,
			Count:    g.count,
		})
	}
	return out
}

// NewGeneratorCatalog constructs a GeneratorCatalog for a built-in packID.
func NewGeneratorCatalog(packID string) (*GeneratorCatalog, bool) {
	for _, g := range builtInGenerators {
		if g.id == packID {
			return &GeneratorCatalog{
				Pack: PackInfo{
					ID:       g.id,
					Name:     g.name,
					Source:   SourceGenerated,
					ReadOnly: true,
					Count:    g.count,
				},
				Protocol:  g.protocol,
				Bits:      g.bits,
				TE:        g.te,
				FreqHz:    g.freqHz,
				Preset:    defaultGenPreset,
				Prefix:    g.prefix,
				HexDigits: g.hexDigits,
				Count:     g.count,
			}, true
		}
	}
	return nil, false
}

// Len returns the total keyspace size of this generator.
func (g *GeneratorCatalog) Len() int { return g.Count }

// SetFrequency overrides the carrier frequency used for generated entries.
func (g *GeneratorCatalog) SetFrequency(freqHz uint32) {
	if freqHz > 0 {
		g.FreqHz = freqHz
	}
}

// SetPreset overrides the Flipper preset string used for generated entries.
func (g *GeneratorCatalog) SetPreset(subPreset string) {
	if subPreset != "" {
		g.Preset = subPreset
	}
}

// EntryAt synthesizes the EntryRef and SubFile for key index idx in O(1).
func (g *GeneratorCatalog) EntryAt(idx int) (EntryRef, bool) {
	if idx < 0 || idx >= g.Count {
		return EntryRef{}, false
	}
	name := fmt.Sprintf("%s_%0*X.sub", g.Prefix, g.HexDigits, idx)
	sf := &subfile.SubFile{
		FileType: "Flipper SubGhz Key File",
		Version:  1,
		FreqHz:   g.FreqHz,
		Preset:   g.Preset,
		Protocol: g.Protocol,
		Bit:      g.Bits,
		Key:      formatGenKeyHex(uint64(idx)),
		TE:       g.TE,
	}
	return EntryRef{
		Name:     name,
		PackID:   g.Pack.ID,
		Source:   SourceGenerated,
		ReadOnly: true,
		File:     sf,
	}, true
}

// ResolveByName parses a generated filename (e.g. "came12_12A.sub") back to its EntryRef in O(1).
func (g *GeneratorCatalog) ResolveByName(name string) (EntryRef, bool) {
	clean, err := SanitizeSubName(name)
	if err != nil {
		return EntryRef{}, false
	}
	lower := strings.ToLower(clean)
	wantPrefix := strings.ToLower(g.Prefix) + "_"
	if !strings.HasPrefix(lower, wantPrefix) || !strings.HasSuffix(lower, ".sub") {
		return EntryRef{}, false
	}
	hexPart := strings.TrimSuffix(strings.TrimPrefix(lower, wantPrefix), ".sub")
	val, err := strconv.ParseUint(hexPart, 16, 32)
	if err != nil || int(val) >= g.Count {
		return EntryRef{}, false
	}
	return g.EntryAt(int(val))
}

func formatGenKeyHex(v uint64) string {
	return fmt.Sprintf("00 00 00 00 00 00 %02X %02X", (v>>8)&0xFF, v&0xFF)
}
