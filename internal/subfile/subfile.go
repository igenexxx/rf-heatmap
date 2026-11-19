package subfile

import (
	"fmt"
	"strconv"
	"strings"
)

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
	lines := strings.Split(data, "\n")
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		k := strings.TrimSpace(line[:colon])
		v := strings.TrimSpace(line[colon+1:])

		switch k {
		case "Filetype":
			sf.FileType = v
		case "Version":
			n, err := strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("subfile: invalid Version %q: %w", v, err)
			}
			sf.Version = n
		case "Frequency":
			n, err := strconv.ParseUint(v, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("subfile: invalid Frequency %q: %w", v, err)
			}
			sf.FreqHz = uint32(n)
		case "Preset":
			sf.Preset = v
		case "Protocol":
			sf.Protocol = v
		case "Bit":
			n, err := strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("subfile: invalid Bit %q: %w", v, err)
			}
			sf.Bit = n
		case "Key":
			sf.Key = v
		case "TE":
			n, err := strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("subfile: invalid TE %q: %w", v, err)
			}
			sf.TE = n
		case "RAW_Data":
			for _, tok := range strings.Fields(v) {
				n, err := strconv.ParseInt(tok, 10, 32)
				if err != nil {
					return nil, fmt.Errorf("subfile: invalid RAW_Data sample %q: %w", tok, err)
				}
				sf.RawData = append(sf.RawData, int32(n))
			}
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
	fmt.Fprintf(&b, "Filetype: %s\n", ft)
	fmt.Fprintf(&b, "Version: %d\n", ver)
	fmt.Fprintf(&b, "Frequency: %d\n", sf.FreqHz)
	fmt.Fprintf(&b, "Preset: %s\n", sf.Preset)
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
	if len(sf.RawData) > 0 {
		const chunkSize = 512
		for i := 0; i < len(sf.RawData); i += chunkSize {
			end := i + chunkSize
			if end > len(sf.RawData) {
				end = len(sf.RawData)
			}
			b.WriteString("RAW_Data:")
			for _, s := range sf.RawData[i:end] {
				fmt.Fprintf(&b, " %d", s)
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}
