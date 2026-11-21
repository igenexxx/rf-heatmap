package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/library"
	"rf-heatmap/internal/rfml"
	"rf-heatmap/internal/subfile"
)

const mcpTransmitTimeout = 10 * time.Second

func loadRefForMCP(ref library.EntryRef) (*subfile.SubFile, error) {
	return library.LoadSubFile(ref)
}

func (s *Server) toolListDatabases() (*toolCallResult, *rpcError) {
	packs := s.store.ListPacks()
	return jsonResult(map[string]any{
		"count": len(packs),
		"packs": packs,
	})
}

func (s *Server) toolPromoteToUser(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		Pack string `json:"pack"`
		Name string `json:"name"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if args.Pack == "" || args.Pack == library.PackIDUser {
		return toolError("pack must specify an external database pack_id (e.g. db:fans or ext:subg)")
	}
	ref, err := s.resolvePackEntry(args.Pack, args.Name)
	if err != nil {
		return toolError(err.Error())
	}
	promoted, err := s.store.PromoteToUser(ref)
	if err != nil {
		return toolError(err.Error())
	}
	return jsonResult(map[string]any{
		"promoted": true,
		"name":     promoted.Name,
		"path":     promoted.Path,
		"source":   string(promoted.Source),
	})
}

func (s *Server) toolTransmitSaved(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		Pack      string `json:"pack"`
		Name      string `json:"name"`
		ConfirmRF bool   `json:"confirm_rf"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if !args.ConfirmRF {
		return toolError("RF transmission requires confirm_rf=true")
	}
	packID := args.Pack
	if packID == "" {
		packID = library.PackIDUser
	}
	ref, err := s.resolvePackEntry(packID, args.Name)
	if err != nil {
		return toolError(err.Error())
	}
	sf, err := library.LoadSubFile(ref)
	if err != nil {
		return toolError(err.Error())
	}
	pulses, err := subfile.EncodePulses(sf)
	if err != nil {
		return toolError(err.Error())
	}
	preset, ok := cc1101.PresetFromSubName(sf.Preset)
	if !ok {
		preset = cc1101.PresetAM650
	}

	simulated := s.radio == nil
	if !simulated {
		ctx, cancel := context.WithTimeout(context.Background(), mcpTransmitTimeout)
		defer cancel()
		s.radioMu.Lock()
		txErr := s.radio.TransmitPulses(ctx, sf.FreqHz, preset, pulses)
		s.radioMu.Unlock()
		if txErr != nil {
			return toolError(fmt.Sprintf("transmit %s: %v", ref.Name, txErr))
		}
	}
	return jsonResult(map[string]any{
		"transmitted": true,
		"simulated":   simulated,
		"pack_id":     packID,
		"name":        ref.Name,
		"freq_hz":     sf.FreqHz,
		"preset":      cc1101.GetPresetSpec(preset).Name,
		"protocol":    sf.Protocol,
		"pulses":      len(pulses),
	})
}

func (s *Server) resolvePackEntry(packID, name string) (library.EntryRef, error) {
	cleanName, err := library.SanitizeSubName(name)
	if err != nil {
		return library.EntryRef{}, err
	}
	if gen, ok := library.NewGeneratorCatalog(packID); ok {
		if ref, found := gen.ResolveByName(cleanName); found {
			return ref, nil
		}
		return library.EntryRef{}, fmt.Errorf("%w: %s in generator %s", errors.New("entry not found"), cleanName, packID)
	}
	entries, err := s.store.ListEntries(packID)
	if err != nil {
		return library.EntryRef{}, err
	}
	for _, ref := range entries {
		if ref.Name == cleanName {
			return ref, nil
		}
	}
	return library.EntryRef{}, fmt.Errorf("%w: %s in pack %s", errors.New("entry not found"), cleanName, packID)
}

func (s *Server) toolRankProbePack(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		Pack   string  `json:"pack"`
		Pulses []int32 `json:"pulses"`
		FreqHz uint32  `json:"freq_hz"`
		Preset string  `json:"preset"`
		TopK   int     `json:"top_k"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if len(args.Pulses) == 0 {
		return toolError("pulses must not be empty")
	}
	entries, err := s.store.ListEntries(args.Pack)
	if err != nil {
		return toolError(err.Error())
	}
	freq := args.FreqHz
	if freq == 0 {
		freq = 433_920_000
	}
	query := rfml.ComputeEmbeddingFromRaw(args.Pulses, freq, args.Preset)
	ranked, best := library.RankEntriesByEmbedding(entries, query, freq)
	topK := args.TopK
	if topK <= 0 || topK > len(ranked) {
		topK = min(10, len(ranked))
	}
	names := make([]string, 0, topK)
	for _, r := range ranked[:topK] {
		names = append(names, r.Name)
	}
	return jsonResult(map[string]any{
		"pack_id":         args.Pack,
		"best_similarity": best,
		"ranked_names":    names,
	})
}
