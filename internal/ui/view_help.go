package ui

import (
	"fmt"
	"strings"

	"rf-heatmap/internal/library"
)

const (
	helpPage1MaxKeys = 6
	helpPageNMaxKeys = 8
	helpSummaryMaxCh = 48
	helpDescMaxCh    = 41
)

// HelpKeyHint describes one available key or softkey on the active screen.
type HelpKeyHint struct {
	Key     string // Short keycap label (<= 7 chars), e.g. "4/Ent", "Z/C", "V"
	Desc    string // Concise English explanation (<= 41 chars)
	Softkey bool   // True for 4..8 bottom-bar softkeys (highlighted in cyan)
}

// ScreenHelp holds context-aware documentation for the active screen and sub-state.
type ScreenHelp struct {
	ScreenTitle string
	Summary     [2]string
	Keys        []HelpKeyHint
}

// PageCount returns how many modal pages are needed to display s cleanly on 320x170.
func (s ScreenHelp) PageCount() int {
	if len(s.Keys) <= helpPage1MaxKeys {
		return 1
	}
	rem := len(s.Keys) - helpPage1MaxKeys
	return 1 + (rem+helpPageNMaxKeys-1)/helpPageNMaxKeys
}

// ActiveScreenHelp returns exact context-aware help for the currently visible screen.
func (a *App) ActiveScreenHelp() ScreenHelp {
	switch {
	case a.ShowSnapshot:
		return helpForSnapshot()
	case a.ShowTrimmer:
		return helpForTrimmer()
	case a.ShowFoxhunt:
		return helpForFoxhunt()
	case a.ShowSensors:
		return helpForSensors()
	case a.ShowSources:
		return helpForSources()
	}
	switch a.Mode {
	case ModeHome:
		return helpForHome(a.Settings.ExpertMode)
	case ModeRead:
		return helpForRead(a.ReadCaptured, a.Settings.ExpertMode)
	case ModeHeatmap:
		return helpForHeatmap(a.Settings.ExpertMode)
	case ModeSaved:
		return helpForSaved(a.Settings.ExpertMode)
	case ModePulse:
		return helpForPulse()
	case ModeDecode:
		return helpForDecode()
	case ModeProbe:
		running := a.Probe != nil && a.Probe.Status == library.ProbeRunning
		return helpForProbe(running)
	case ModeSettings:
		return helpForSettings(a.Settings.ExpertMode)
	default:
		return helpForHome(a.Settings.ExpertMode)
	}
}

func (a *App) handleHelpKey(act KeyAction) {
	pages := a.ActiveScreenHelp().PageCount()
	switch act {
	case KeyLeft, KeyUp:
		if pages > 1 {
			a.HelpPage = (a.HelpPage + pages - 1) % pages
		}
	case KeyRight, KeyDown, KeySpace, KeyTab, KeyNav5:
		if pages > 1 {
			a.HelpPage = (a.HelpPage + 1) % pages
		}
	case KeyQuit, KeyBack, KeyEnter, KeyNav4, KeyNav8:
		a.ShowHelp = false
	}
}

func (a *App) drawHelpModal(c *Canvas) {
	h := a.ActiveScreenHelp()
	totalPages := h.PageCount()
	page := a.HelpPage % totalPages

	// Clean full-screen backdrop eliminates half-clipped underlying chrome on 320x170.
	c.Clear(ColBg)

	const (
		mx, my, mw, mh = 4, 4, 312, 162
	)
	c.FillRect(mx, my, mw, mh, ColPanel)
	c.DrawRect(mx, my, mw, mh, ColCyan)
	c.FillRect(mx+1, my+1, mw-2, 14, ColNavActive)

	title := "HELP: " + truncate(h.ScreenTitle, 32)
	c.DrawText(mx+6, my+4, title, ColText, 1)
	pageLbl := fmt.Sprintf("%d/%d", page+1, totalPages)
	c.DrawText(mx+mw-len(pageLbl)*6-6, my+4, pageLbl, ColAmber, 1)

	if page == 0 {
		drawHelpPageOne(c, h, mx, my)
	} else {
		drawHelpOverflowPage(c, h, page, mx, my)
	}

	c.HLine(mx+2, my+mh-17, mw-4, ColBorder)
	footer := "[H / Enter / Esc] Close Help"
	if totalPages > 1 {
		footer = "[Z/C] Page   [H / Enter / Esc] Close"
	}
	c.DrawTextCentered(ScreenW/2, my+mh-12, footer, ColAmber, 1)
}

func drawHelpPageOne(c *Canvas, h ScreenHelp, mx, my int) {
	// Screen summary card with left cyan accent.
	c.FillRect(mx+5, my+18, 302, 26, ColPanelAlt)
	c.FillRect(mx+5, my+18, 2, 26, ColCyan)
	c.DrawText(mx+10, my+21, truncate(h.Summary[0], helpSummaryMaxCh), ColText, 1)
	if strings.TrimSpace(h.Summary[1]) != "" {
		c.DrawText(mx+10, my+32, truncate(h.Summary[1], helpSummaryMaxCh), ColMuted, 1)
	}

	c.DrawText(mx+6, my+48, "AVAILABLE CONTROLS", ColCyan, 1)
	c.HLine(mx+118, my+51, 188, ColBorder)

	end := min(len(h.Keys), helpPage1MaxKeys)
	for i := range end {
		drawHelpKeyRow(c, mx+5, my+59+i*14, h.Keys[i])
	}
}

func drawHelpOverflowPage(c *Canvas, h ScreenHelp, page, mx, my int) {
	c.DrawText(mx+6, my+19, "MORE CONTROLS", ColCyan, 1)
	c.HLine(mx+88, my+22, 218, ColBorder)

	start := helpPage1MaxKeys + (page-1)*helpPageNMaxKeys
	end := min(len(h.Keys), start+helpPageNMaxKeys)
	for i := start; i < end; i++ {
		drawHelpKeyRow(c, mx+5, my+31+(i-start)*14, h.Keys[i])
	}
}

func drawHelpKeyRow(c *Canvas, x, y int, k HelpKeyHint) {
	const badgeW = 46
	borderCol, keyCol := ColAmber, ColAmber
	if k.Softkey {
		borderCol, keyCol = ColCyan, ColCyan
	}
	c.FillRect(x, y, badgeW, 11, ColBg)
	c.DrawRect(x, y, badgeW, 11, borderCol)
	c.DrawTextCentered(x+badgeW/2, y+2, truncate(k.Key, 7), keyCol, 1)
	c.DrawText(x+badgeW+5, y+2, truncate(k.Desc, helpDescMaxCh), ColText, 1)
}

func helpForHome(expert bool) ScreenHelp {
	summary := [2]string{
		"Main launcher for Sub-GHz RF tools (Simple).",
		"Press 5 EXP to unlock +4 engineering labs.",
	}
	if expert {
		summary = [2]string{
			"Main launcher in Expert Mode (all 9 tools).",
			"Includes Pulse Scope, Decoder, Probe & Fox.",
		}
	}
	return ScreenHelp{
		ScreenTitle: "HOME MENU",
		Summary:     summary,
		Keys: []HelpKeyHint{
			{"F/X", "Move selection up / down", false},
			{"4/Ent", "Open highlighted tool", true},
			{"5 EXP", "Toggle Simple / Expert Mode (saved)", true},
			{"6 READ", "Quick-open Read & Replay wizard", true},
			{"7 SEND", "Quick-open My Captures library", true},
			{"Esc/8", "Exit prompt (Enter/Esc confirms)", true},
			{"Q", "Quit RF Heatmap immediately", false},
			{"H", "Show or hide this Help window", false},
		},
	}
}

func helpForRead(captured, expert bool) ScreenHelp {
	if !captured {
		keys := []HelpKeyHint{
			{"4 AUTO", "Toggle Auto-Scan or fixed ISM freq", true},
			{"5 MOD", "Cycle modem (AM650/AM270/FM238/FM476)", true},
			{"6/Ent", "Start / stop manual RAW recording", true},
			{"Z/C", "Step fixed freq (433/868/315/915 MHz)", false},
		}
		if expert {
			keys = append(keys, HelpKeyHint{"7 LAB", "Jump to Expert Pulse Scope lab", true})
		}
		keys = append(keys, HelpKeyHint{"Esc/8", "Return to Home Menu", true})
		return ScreenHelp{
			ScreenTitle: "READ & REPLAY (LISTEN)",
			Summary: [2]string{
				"Auto-scans ISM bands & locks onto remotes.",
				"PulseNet-RF rejects noise & freezes capture.",
			},
			Keys: keys,
		}
	}
	return ScreenHelp{
		ScreenTitle: "READ & REPLAY (CAPTURED)",
		Summary: [2]string{
			"Signal captured! Test replay, inspect, or save.",
			"Rolling codes require 2-step TX confirmation.",
		},
		Keys: []HelpKeyHint{
			{"4/Ent", "Replay captured signal via CC1101", true},
			{"5 SAVE", "Save .sub & open My Captures", true},
			{"6/Spc", "Discard & listen for next remote", true},
			{"7/V", "Open Signal Snapshot & us Histogram", true},
			{"B", "Switch frequency & listen again", false},
			{"Esc/8", "Return to Home Menu", true},
		},
	}
}

func helpForSnapshot() ScreenHelp {
	return ScreenHelp{
		ScreenTitle: "SIGNAL SNAPSHOT",
		Summary: [2]string{
			"OOK square-wave trace & +HI/-LO us histogram.",
			"Shows K-Means S/L centroids, ratio & duty %.",
		},
		Keys: []HelpKeyHint{
			{"4/Spc", "Cycle view: SPLIT -> WAVE -> HIST", true},
			{"5/F/X", "Zoom waveform (1x / 2x / 4x / 8x)", true},
			{"Z/C", "Pan horizontally across signal time", false},
			{"6/Ent", "Transmit (TX) this signal immediately", true},
			{"7/T", "Open RAW Trimmer for this capture", true},
			{"Esc/8", "Return to previous screen", true},
		},
	}
}

func helpForTrimmer() ScreenHelp {
	return ScreenHelp{
		ScreenTitle: "RAW TRIMMER",
		Summary: [2]string{
			"Strip leading/trailing noise from RAW pulses.",
			"Repeats 1-10x & saves atomically to User lib.",
		},
		Keys: []HelpKeyHint{
			{"4 HDL", "Switch active handle [START] / [END]", true},
			{"Z/C", "Move selected START or END boundary", false},
			{"5 AUTO", "Auto-detect clean burst boundaries", true},
			{"F/X", "Adjust frame repeats (1x to 10x)", false},
			{"6/Ent", "Preview transmit trimmed capture", true},
			{"7 SAVE", "Save *_trim.sub to User captures", true},
			{"V", "Inspect trimmed slice in Snapshot", false},
			{"Esc/8", "Return without quitting app", true},
		},
	}
}

func helpForHeatmap(expert bool) ScreenHelp {
	k7 := HelpKeyHint{"7 FOX", "Open Foxhunt Geiger on cursor freq", true}
	k8 := HelpKeyHint{"Esc/8", "Return to Home Menu", true}
	if expert {
		k7 = HelpKeyHint{"7/M", "Cycle CC1101 modulation preset", true}
		k8 = HelpKeyHint{"8/Tab", "Next Expert tab (Esc = Home)", true}
	}
	return ScreenHelp{
		ScreenTitle: "SPECTRUM MAP",
		Summary: [2]string{
			"36-bin ISM sweep, Inferno waterfall & Peak-Hold.",
			"Smart Squelch, EMI room diff & SpectrumNet-2D.",
		},
		Keys: []HelpKeyHint{
			{"4/Spc", "Pause / resume spectrum sweep", true},
			{"5/F", "Cycle ISM band (433/868/315/915/ALL)", true},
			{"6/Ent", "Lock peak/cursor freq & capture", true},
			k7,
			{"Z/C", "Move tuning cursor left / right", false},
			{"X", "Clear amber Peak-Hold markers", false},
			{"B / D", "Save room baseline / toggle EMI DIFF", false},
			{"G", "Launch Foxhunt Geiger on cursor freq", false},
			{"S / L", "Toggle Auto Squelch / Auto-Lock", false},
			k8,
		},
	}
}

func helpForSaved(expert bool) ScreenHelp {
	if !expert {
		return ScreenHelp{
			ScreenTitle: "MY CAPTURES",
			Summary: [2]string{
				"Personal .sub files (~/rf-heatmap/captures).",
				"Replay, inspect waveform, trim, or delete.",
			},
			Keys: []HelpKeyHint{
				{"F/X", "Select saved .sub capture", false},
				{"4/Ent", "Send / replay selected .sub signal", true},
				{"5/T", "Open RAW Trimmer on selected file", true},
				{"6 NEW", "Capture a new remote (Read & Replay)", true},
				{"7/Del", "Delete selected capture", true},
				{"V", "Signal Snapshot & us Histogram", false},
				{"Esc/8", "Return to Home Menu", true},
			},
		}
	}
	return ScreenHelp{
		ScreenTitle: "MY CAPTURES (EXPERT)",
		Summary: [2]string{
			"Manage read-write User .sub library safely.",
			"Supports batch [x] delete & ML noise purge.",
		},
		Keys: []HelpKeyHint{
			{"F/X", "Select saved .sub capture", false},
			{"4/Ent", "Transmit selected .sub via CC1101", true},
			{"5 LOAD", "Load frequency into Pulse Scope", true},
			{"6/Del", "Delete selected (2-step if batch)", true},
			{"7 PRG", "Purge ML-detected noise (press 2x)", true},
			{"V", "Signal Snapshot & us Histogram", false},
			{"T", "Open RAW Trimmer for RAW .sub", false},
			{"Spc/D", "Mark/unmark [x] for batch delete", false},
			{"8/Tab", "Next Expert tab (Esc = Home)", true},
		},
	}
}

func helpForSensors() ScreenHelp {
	return ScreenHelp{
		ScreenTitle: "SENSORS (TPMS & WEATHER)",
		Summary: [2]string{
			"Decodes car TPMS (Schrader/Toyota GFSK99)",
			"and 433 MHz weather (Oregon/Acurite/LaCrosse).",
		},
		Keys: []HelpKeyHint{
			{"4/Spc", "Pause / resume live sensor updates", true},
			{"5 UNIT", "Toggle metric (kPa/C) or PSI/F", true},
			{"6/B/M", "Switch profile (TPMS 433/315, Wthr)", true},
			{"7 CLR", "Clear received sensor readings", true},
			{"F/X", "Scroll through detected sensors", false},
			{"Esc/8", "Exit & restore previous freq/modem", true},
		},
	}
}

func helpForPulse() ScreenHelp {
	return ScreenHelp{
		ScreenTitle: "PULSE SCOPE",
		Summary: [2]string{
			"Live single-channel RSSI scope & RAW recorder.",
			"Shows FREQEST drift, Smart Squelch & Foxhunt.",
		},
		Keys: []HelpKeyHint{
			{"4/Spc", "Start / stop RAW GDO0 pulse capture", true},
			{"5/Ent", "Replay last captured RAW or key", true},
			{"6 SAVE", "Save RAW .sub (noise guard active)", true},
			{"7/M", "Cycle CC1101 modulation preset", true},
			{"Z/C", "Fine-tune frequency (-/+ 50 kHz)", false},
			{"F/X", "Adjust Smart Squelch K or dBm limit", false},
			{"V", "Open Signal Snapshot & us Histogram", false},
			{"T / G", "Open RAW Trimmer / Foxhunt Geiger", false},
			{"S / B", "Toggle Auto Squelch / cycle band", false},
			{"8/Tab", "Next Expert tab (Esc = Home)", true},
		},
	}
}

func helpForDecode() ScreenHelp {
	return ScreenHelp{
		ScreenTitle: "OOK DECODER",
		Summary: [2]string{
			"Decodes Princeton/CAME/Linear/KeeLoq/Somfy.",
			"3-pass ML/PulseNet rescue + Rolling TX guard.",
		},
		Keys: []HelpKeyHint{
			{"4/Spc", "Pause / resume live OOK decoder", true},
			{"5/Ent", "Replay last key (2-step on rolling)", true},
			{"6 SAVE", "Save decoded key as Flipper .sub", true},
			{"7 CLR", "Clear decoded log & rolling state", true},
			{"R", "Open live TPMS & Weather Sensors", false},
			{"B / M", "Cycle ISM band / modulation preset", false},
			{"S", "Toggle Auto Squelch (ASQ)", false},
			{"8/Tab", "Next Expert tab (Esc = Home)", true},
		},
	}
}

func helpForProbe(running bool) ScreenHelp {
	if running {
		return ScreenHelp{
			ScreenTitle: "AUTO-PROBE (RUNNING)",
			Summary: [2]string{
				"Auto-transmitting signals from active pack.",
				"Stop immediately on reaction, then rewind >/*.",
			},
			Keys: []HelpKeyHint{
				{"4/Spc", "STOP immediately & pause session", true},
				{"Enter", "STOP immediately (same as 4/Space)", false},
				{"5/Z", "Faster probe interval (down to 0.05s)", true},
				{"6/C", "Slower probe interval (up to 5.0s)", true},
				{"7 PACK", "Stop & switch to next signal pack", true},
				{"Esc/8", "Stop & return to Home / next tab", true},
			},
		}
	}
	return ScreenHelp{
		ScreenTitle: "AUTO-PROBE",
		Summary: [2]string{
			"Step through DB packs, GEN:CAME-12/Linear-10,",
			"or USER captures; rewind via > and * markers.",
		},
		Keys: []HelpKeyHint{
			{"4/Spc", "Start or resume Auto-Probe sweep", true},
			{"5/Ent", "Single-fire highlighted candidate", true},
			{"6 SAVE", "Copy winning signal to User library", true},
			{"7 PACK", "Cycle DB:* -> GEN:* -> USER packs", true},
			{"F/X", "Step cursor up/down (rewind >/*)", false},
			{"Z/C", "Adjust probe interval (0.05s-5.0s)", false},
			{"R", "ML-rank DB pack by last capture", false},
			{"B / M", "Override GEN:* freq / modulation", false},
			{"D/Del", "Mark [x] / delete DB file (ignored)", false},
			{"8/Tab", "Next Expert tab (Esc = Home)", true},
		},
	}
}

func helpForFoxhunt() ScreenHelp {
	return ScreenHelp{
		ScreenTitle: "FOXHUNT GEIGER",
		Summary: [2]string{
			"Locked-frequency RSSI finder with Peak-Hold.",
			"Kalman ^CLOSER/vFARTHER & proportional ticks.",
		},
		Keys: []HelpKeyHint{
			{"4 RST", "Reset amber Peak-Hold marker", true},
			{"5 MUTE", "Toggle Geiger audio clicker ON/OFF", true},
			{"6/B", "Cycle ISM band preset", true},
			{"7/M", "Cycle CC1101 modulation preset", true},
			{"Z/C", "Fine-tune frequency (-/+ 50 kHz)", false},
			{"Esc/8", "Exit Foxhunt to previous screen", true},
		},
	}
}

func helpForSettings(expert bool) ScreenHelp {
	if !expert {
		return ScreenHelp{
			ScreenTitle: "SETTINGS (SIMPLE)",
			Summary: [2]string{
				"Configure Expert Mode, TX Power, opt-in SSH",
				"MCP Server, DB Sync & Auto Squelch.",
			},
			Keys: []HelpKeyHint{
				{"F/X", "Select setting row", false},
				{"4/Ent", "Toggle / activate selected setting", true},
				{"Z/C", "Adjust TX Power (-30 to +10 dBm)", false},
				{"5 SAVE", "Save config.json explicitly", true},
				{"6 SYNC", "Run incremental remote DB sync", true},
				{"Esc/8", "Return to Home Menu", true},
			},
		}
	}
	return ScreenHelp{
		ScreenTitle: "SETTINGS (EXPERT)",
		Summary: [2]string{
			"Full radio, ML squelch, TX PATABLE, remote DB",
			"sources & opt-in MCP Server configuration.",
		},
		Keys: []HelpKeyHint{
			{"F/X", "Select setting row (9 options)", false},
			{"4/Ent", "Toggle setting or open Manage DBs", true},
			{"Z/C", "Adjust Squelch K or TX Power dBm", false},
			{"5 SAVE", "Save ~/.config/rf-heatmap/config.json", true},
			{"6 RST", "Restore safe factory defaults", true},
			{"7 HELP", "Open this context Help modal", true},
			{"Esc/8", "Return to Home Menu", true},
		},
	}
}

func helpForSources() ScreenHelp {
	return ScreenHelp{
		ScreenTitle: "MANAGE DB SOURCES",
		Summary: [2]string{
			"Manage GitHub/HTTP .sub sources & local packs.",
			"Supports [x] multi-select & 2-step deletion.",
		},
		Keys: []HelpKeyHint{
			{"F/X", "Move cursor through sources/packs", false},
			{"Spc/D", "Mark/unmark [x] for batch actions", false},
			{"4 ON", "Enable or disable selected source(s)", true},
			{"5 DIR", "Delete local pack folder (press 2x)", true},
			{"6/Del", "Delete source + folder (press 2x)", true},
			{"7 SYNC", "Run incremental delta sync now", true},
			{"Esc/8", "Back to Settings & refresh packs", true},
		},
	}
}
