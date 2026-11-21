package ui

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
)

// KeyAction represents a logical user action mapped from CardputerZero hardware keys.
type KeyAction int

const (
	KeyNone KeyAction = iota
	KeyQuit
	KeyTab
	KeyEnter
	KeySpace
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyNav4
	KeyNav5
	KeyNav6
	KeyNav7
	KeyNav8
	KeyBand
	KeyMod
	KeyHelp
	KeyDelete
	KeyAutoSquelch
	KeyAutoLock
)

// Linux input_event (64-bit arm64): tv_sec(8), tv_usec(8), type(2), code(2), value(4) = 24 bytes.
const (
	evKey          uint16 = 0x01
	keyValPress    int32  = 1
	keyValRepeat   int32  = 2
	codeEsc        uint16 = 1
	code1          uint16 = 2
	code4          uint16 = 5
	code5          uint16 = 6
	code6          uint16 = 7
	code7          uint16 = 8
	code8          uint16 = 9
	codeBackspace  uint16 = 14
	codeTab        uint16 = 15
	codeQ          uint16 = 16
	codeR          uint16 = 19
	codeEnter      uint16 = 28
	codeS          uint16 = 31
	codeD          uint16 = 32
	codeF          uint16 = 33
	codeH          uint16 = 35
	codeL          uint16 = 38
	codeZ          uint16 = 44
	codeX          uint16 = 45
	codeC          uint16 = 46
	codeB          uint16 = 48
	codeM          uint16 = 50
	codeSpace      uint16 = 57
	codeUp         uint16 = 103
	codeLeft       uint16 = 105
	codeRight      uint16 = 106
	codeDown       uint16 = 108
	codeDelete     uint16 = 111
)

var linuxKeyMap = map[uint16]KeyAction{
	codeEsc:       KeyQuit,
	codeQ:         KeyQuit,
	codeTab:       KeyTab,
	codeEnter:     KeyEnter,
	codeSpace:     KeySpace,
	codeUp:        KeyUp,
	codeF:         KeyUp,
	codeDown:      KeyDown,
	codeX:         KeyDown,
	codeLeft:      KeyLeft,
	codeZ:         KeyLeft,
	codeRight:     KeyRight,
	codeC:         KeyRight,
	code4:         KeyNav4,
	code5:         KeyNav5,
	code6:         KeyNav6,
	code7:         KeyNav7,
	code8:         KeyNav8,
	codeB:         KeyBand,
	codeM:         KeyMod,
	codeH:         KeyHelp,
	codeS:         KeyAutoSquelch,
	codeL:         KeyAutoLock,
	codeDelete:    KeyDelete,
	codeBackspace: KeyDelete,
}

// MapLinuxKeyCode converts a Linux evdev keycode into a KeyAction.
func MapLinuxKeyCode(code uint16) KeyAction {
	return linuxKeyMap[code]
}

// StartEvdevReader opens CardputerZero keyboard evdev nodes and emits KeyActions.
func StartEvdevReader() (<-chan KeyAction, func()) {
	ch := make(chan KeyAction, 32)
	done := make(chan struct{})

	var files []*os.File
	for _, p := range findKeyboardEvdevPaths() {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		files = append(files, f)
		go readEvdevLoop(f, done, ch)
	}

	stop := func() {
		close(done)
		for _, f := range files {
			_ = f.Close()
		}
	}
	return ch, stop
}

func readEvdevLoop(dev *os.File, done <-chan struct{}, ch chan<- KeyAction) {
	const inputEventSize = 24
	buf := make([]byte, inputEventSize)
	for {
		select {
		case <-done:
			return
		default:
		}
		n, err := dev.Read(buf)
		if err != nil || n < inputEventSize {
			return
		}
		evType := binary.LittleEndian.Uint16(buf[16:18])
		evCode := binary.LittleEndian.Uint16(buf[18:20])
		evVal := int32(binary.LittleEndian.Uint32(buf[20:24]))
		if evType != evKey || (evVal != keyValPress && evVal != keyValRepeat) {
			continue
		}
		if act := MapLinuxKeyCode(evCode); act != KeyNone {
			select {
			case ch <- act:
			default:
			}
		}
	}
}

func findKeyboardEvdevPaths() []string {
	// Prefer CardputerZero's TCA8418 I2C keyboard if present, plus any /dev/input/event*.
	var out []string
	byPath, _ := filepath.Glob("/dev/input/by-path/*i2c*-event")
	out = append(out, byPath...)
	if len(out) == 0 {
		all, _ := filepath.Glob("/dev/input/event*")
		for _, p := range all {
			if !strings.Contains(p, "mouse") {
				out = append(out, p)
			}
		}
	}
	return out
}
