package subfile

import (
	"fmt"
	"strconv"
	"strings"
)

const rawDataChunkSize = 512

// SubFile represents a Flipper Zero compatible .sub file (Key or RAW).
type SubFile struct {
	FileType string // "Flipper SubGhz Key File" or "Flipper SubGhz RAW File"
	Version  int
	FreqHz   uint32
	Preset   string
	Protocol string
	Bit      int
	Key      string
	TE       int
	RawData  []int32
}

// Parse parses a Flipper Zero .sub text document.
func Parse(data string) (*SubFile, error) {
	sf := &SubFile{Version: 1}
	for rawLine := range strings.SplitSeq(data, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if err := applySubField(sf, strings.TrimSpace(k), strings.TrimSpace(v)); err != nil {
			return nil, err
		}
	}
	if sf.FileType == "" {
		return nil, fmt.Errorf("subfile: missing Filetype header")
	}
	if sf.FreqHz == 0 {
		return nil, fmt.Errorf("subfile: missing or zero Frequency")
	}
	return sf, nil
}

func applySubField(sf *SubFile, key, val string) error {
	switch key {
	case "Filetype":
		sf.FileType = val
	case "Version":
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("subfile: invalid Version %q: %w", val, err)
		}
		sf.Version = n
	case "Frequency":
		n, err := strconv.ParseUint(val, 10, 32)
		if err != nil {
			return fmt.Errorf("subfile: invalid Frequency %q: %w", val, err)
		}
		sf.FreqHz = uint32(n)
	case "Preset":
		sf.Preset = val
	case "Protocol":
		sf.Protocol = val
	case "Bit":
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("subfile: invalid Bit %q: %w", val, err)
		}
		sf.Bit = n
	case "Key":
		sf.Key = val
	case "TE":
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("subfile: invalid TE %q: %w", val, err)
		}
		sf.TE = n
	case "RAW_Data":
		return parseRawDataLine(sf, val)
	}
	return nil
}

func parseRawDataLine(sf *SubFile, val string) error {
	for _, tok := range strings.Fields(val) {
		n, err := strconv.ParseInt(tok, 10, 32)
		if err != nil {
			return fmt.Errorf("subfile: invalid RAW_Data sample %q: %w", tok, err)
		}
		sf.RawData = append(sf.RawData, int32(n))
	}
	return nil
}

// Format serializes a SubFile into Flipper Zero .sub format.
func (sf *SubFile) Format() string {
	var b strings.Builder
	ft := sf.FileType
	if ft == "" {
		if len(sf.RawData) > 0 && sf.Protocol == "" {
			ft = "Flipper SubGhz RAW File"
		} else {
			ft = "Flipper SubGhz Key File"
		}
	}
	ver := sf.Version
	if ver == 0 {
		ver = 1
	}
	fmt.Fprintf(&b, "Filetype: %s\nVersion: %d\nFrequency: %d\nPreset: %s\n", ft, ver, sf.FreqHz, sf.Preset)
	if sf.Protocol != "" {
		fmt.Fprintf(&b, "Protocol: %s\n", sf.Protocol)
	} else if len(sf.RawData) > 0 {
		b.WriteString("Protocol: RAW\n")
	}
	if sf.Bit > 0 {
		fmt.Fprintf(&b, "Bit: %d\n", sf.Bit)
	}
	if sf.Key != "" {
		fmt.Fprintf(&b, "Key: %s\n", sf.Key)
	}
	if sf.TE > 0 {
		fmt.Fprintf(&b, "TE: %d\n", sf.TE)
	}
	writeRawDataChunks(&b, sf.RawData)
	return b.String()
}

func writeRawDataChunks(b *strings.Builder, raw []int32) {
	for i := 0; i < len(raw); i += rawDataChunkSize {
		end := min(i+rawDataChunkSize, len(raw))
		b.WriteString("RAW_Data:")
		for _, s := range raw[i:end] {
			fmt.Fprintf(b, " %d", s)
		}
		b.WriteByte('\n')
	}
}
