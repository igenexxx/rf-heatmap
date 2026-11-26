package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"rf-heatmap/internal/config"
	"rf-heatmap/internal/dbsync"
	"rf-heatmap/internal/library"
	"rf-heatmap/internal/rfml"
)

const (
	// ProtocolVersion is the MCP JSON-RPC protocol version supported by this server.
	ProtocolVersion = "2024-11-05"

	errCodeParseError     = -32700
	errCodeMethodNotFound = -32601
	errCodeInvalidParams  = -32602
)

// Options configures a new MCP Server.
type Options struct {
	Radio          RadioBackend // nil runs in simulator mode
	CapturesDir    string
	DatabasesDir   string   // read-only external .sub packs root (~/rf-heatmap/databases)
	SourcesPath    string   // JSON registry of remote .sub database sources
	Syncer         *dbsync.Syncer
	ExtraSavedDirs []string // nil defaults to [~/subg/files]; set to []string{} to disable
	ConfigPath     string
	BaselinesDir   string
	Settings       config.Settings
	SettleDelay    time.Duration // RX settle delay per hop (default 1200µs when Radio != nil)
}

// Server implements a pure-Go JSON-RPC 2.0 MCP server over stdio.
type Server struct {
	radio        RadioBackend
	capturesDir  string
	databasesDir string
	sourcesPath  string
	syncer       *dbsync.Syncer
	extraDirs    []string
	store        *library.Store
	configPath   string
	baselinesDir string
	settings     config.Settings
	settleDelay  time.Duration

	radioMu        sync.Mutex
	writeMu        sync.Mutex
	foxhuntTracker *rfml.FoxhuntTracker
}

// NewServer creates a configured MCP server instance.
func NewServer(opts Options) *Server {
	extra := opts.ExtraSavedDirs
	if extra == nil {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			extra = []string{filepath.Join(home, "subg", "files")}
		}
	}
	settle := opts.SettleDelay
	if settle == 0 && opts.Radio != nil {
		settle = 1200 * time.Microsecond
	}
	store := library.NewStore(opts.CapturesDir, opts.DatabasesDir, extra)
	sourcesPath := opts.SourcesPath
	if sourcesPath == "" {
		sourcesPath = dbsync.DefaultSourcesPath()
	}
	syncer := opts.Syncer
	if syncer == nil {
		syncer = dbsync.NewSyncer(opts.DatabasesDir)
	}
	baselinesDir := opts.BaselinesDir
	if baselinesDir == "" {
		baselinesDir = rfml.DefaultBaselinesDir()
	}
	return &Server{
		radio: opts.Radio, capturesDir: opts.CapturesDir, databasesDir: opts.DatabasesDir,
		sourcesPath: sourcesPath, syncer: syncer, extraDirs: extra, store: store,
		configPath: opts.ConfigPath, baselinesDir: baselinesDir,
		settings: opts.Settings.Normalize(), settleDelay: settle,
	}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolCallResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ServeStdio reads newline-delimited JSON-RPC 2.0 requests from r and writes responses to w.
func (s *Server) ServeStdio(ctx context.Context, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		resp := s.handleLine([]byte(line))
		if resp == nil {
			continue
		}
		if err := s.writeResponse(w, resp); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func (s *Server) writeResponse(w io.Writer, resp *rpcResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = w.Write(data)
	return err
}

func (s *Server) handleLine(line []byte) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return rpcErrorResp(json.RawMessage("null"), errCodeParseError, "parse error: "+err.Error())
	}
	if isNotification(req) {
		return nil
	}
	id := req.ID
	if len(id) == 0 {
		id = json.RawMessage("null")
	}

	switch req.Method {
	case "initialize":
		return s.handleInitialize(id)
	case "ping":
		return rpcOKResp(id, map[string]any{})
	case "tools/list":
		return rpcOKResp(id, map[string]any{"tools": s.toolDefinitions()})
	case "tools/call":
		return s.handleToolCall(id, req.Params)
	default:
		return rpcErrorResp(id, errCodeMethodNotFound, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func isNotification(req rpcRequest) bool {
	return req.Method == "notifications/initialized" ||
		(strings.HasPrefix(req.Method, "notifications/") && len(req.ID) == 0)
}

func (s *Server) handleInitialize(id json.RawMessage) *rpcResponse {
	return rpcOKResp(id, map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "rf-heatmap", "version": "0.4.0"},
	})
}

func (s *Server) handleToolCall(id, rawParams json.RawMessage) *rpcResponse {
	var params toolCallParams
	if err := json.Unmarshal(rawParams, &params); err != nil || params.Name == "" {
		return rpcErrorResp(id, errCodeInvalidParams, "invalid tools/call params")
	}
	res, rpcErr := s.dispatchTool(params.Name, params.Arguments)
	if rpcErr != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: id, Error: rpcErr}
	}
	return rpcOKResp(id, res)
}

func rpcOKResp(id json.RawMessage, result any) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func rpcErrorResp(id json.RawMessage, code int, msg string) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

func (s *Server) dispatchTool(name string, rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	switch name {
	case "rf_get_status":
		return s.toolGetStatus()
	case "rf_sweep_band":
		return s.toolSweepBand(rawArgs)
	case "rf_read_rssi":
		return s.toolReadRSSI(rawArgs)
	case "rf_decode_ook":
		return s.toolDecodeOOK(rawArgs)
	case "rf_analyze_pulses":
		return s.toolAnalyzePulses(rawArgs)
	case "rf_classify_signal":
		return s.toolClassifySignal(rawArgs)
	case "rf_match_fingerprint":
		return s.toolMatchFingerprint(rawArgs)
	case "rf_foxhunt_track":
		return s.toolFoxhuntTrack(rawArgs)
	case "rf_list_saved":
		return s.toolListSaved(rawArgs)
	case "rf_list_databases":
		return s.toolListDatabases()
	case "rf_promote_to_user":
		return s.toolPromoteToUser(rawArgs)
	case "rf_transmit_saved":
		return s.toolTransmitSaved(rawArgs)
	case "rf_list_sources":
		return s.toolListSources()
	case "rf_add_source":
		return s.toolAddSource(rawArgs)
	case "rf_remove_source":
		return s.toolRemoveSource(rawArgs)
	case "rf_sync_sources":
		return s.toolSyncSources(rawArgs)
	case "rf_delete_pack_files":
		return s.toolDeletePackFiles(rawArgs)
	case "rf_rank_probe_pack":
		return s.toolRankProbePack(rawArgs)
	case "rf_read_saved":
		return s.toolReadSaved(rawArgs)
	case "rf_save_capture":
		return s.toolSaveCapture(rawArgs)
	case "rf_delete_saved":
		return s.toolDeleteSaved(rawArgs)
	case "rf_purge_noise":
		return s.toolPurgeNoise(rawArgs)
	case "rf_analyze_rolling_code":
		return s.toolAnalyzeRollingCode(rawArgs)
	case "rf_decode_sensor":
		return s.toolDecodeSensor(rawArgs)
	case "rf_foxhunt_sample":
		return s.toolFoxhuntSample(rawArgs)
	case "rf_trim_raw":
		return s.toolTrimRaw(rawArgs)
	case "rf_signal_snapshot":
		return s.toolSignalSnapshot(rawArgs)
	case "rf_measure_freq_offset":
		return s.toolMeasureFreqOffset(rawArgs)
	case "rf_set_tx_power":
		return s.toolSetTXPower(rawArgs)
	case "rf_wor_listen":
		return s.toolWORListen(rawArgs)
	case "rf_send_packet":
		return s.toolSendPacket(rawArgs)
	case "rf_recv_packet":
		return s.toolRecvPacket(rawArgs)
	case "rf_decode_pocsag":
		return s.toolDecodePOCSAG(rawArgs)
	case "rf_decode_wmbus":
		return s.toolDecodeWMBus(rawArgs)
	default:
		if res, err, ok := s.dispatchBaselineTool(name, rawArgs); ok {
			return res, err
		}
		if res, err, ok := s.dispatchNeuralMLTool(name, rawArgs); ok {
			return res, err
		}
		return nil, &rpcError{Code: errCodeInvalidParams, Message: fmt.Sprintf("unknown tool: %s", name)}
	}
}

func jsonResult(v any) (*toolCallResult, *rpcError) {
	data, err := json.Marshal(v)
	if err != nil {
		return &toolCallResult{
			Content: []toolContent{{Type: "text", Text: err.Error()}},
			IsError: true,
		}, nil
	}
	return &toolCallResult{
		Content: []toolContent{{Type: "text", Text: string(data)}},
	}, nil
}

func toolError(msg string) (*toolCallResult, *rpcError) {
	return &toolCallResult{
		Content: []toolContent{{Type: "text", Text: msg}},
		IsError: true,
	}, nil
}

func decodeArgs(raw json.RawMessage, dst any) *rpcError {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return &rpcError{Code: errCodeInvalidParams, Message: "invalid tool arguments: " + err.Error()}
	}
	return nil
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}
