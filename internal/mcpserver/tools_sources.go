package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"rf-heatmap/internal/dbsync"
)

const mcpSyncTimeout = 60 * time.Second

func (s *Server) toolListSources() (*toolCallResult, *rpcError) {
	list, err := dbsync.LoadSources(s.sourcesPath)
	if err != nil {
		return toolError(err.Error())
	}
	if list == nil {
		list = []dbsync.DatabaseSource{}
	}
	return jsonResult(map[string]any{
		"count":   len(list),
		"sources": list,
	})
}

func (s *Server) toolAddSource(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		GitHubURL string `json:"github_url"`
		Owner     string `json:"owner"`
		Repo      string `json:"repo"`
		Branch    string `json:"branch"`
		Subpath   string `json:"subpath"`
		URL       string `json:"url"`
		Prune     bool   `json:"prune"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	src := dbsync.DatabaseSource{
		ID:      args.ID,
		Type:    dbsync.SourceType(strings.ToLower(strings.TrimSpace(args.Type))),
		Owner:   args.Owner,
		Repo:    args.Repo,
		Branch:  args.Branch,
		Subpath: args.Subpath,
		URL:     args.URL,
		Enabled: true,
		Prune:   args.Prune,
	}
	if src.Type == dbsync.SourceGitHub && args.GitHubURL != "" {
		owner, repo, branch, sub, err := dbsync.ParseGitHubURL(args.GitHubURL)
		if err != nil {
			return toolError(err.Error())
		}
		src.Owner, src.Repo, src.Branch, src.Subpath = owner, repo, branch, sub
	}
	saved, err := dbsync.UpsertSource(s.sourcesPath, src)
	if err != nil {
		return toolError(err.Error())
	}
	return jsonResult(map[string]any{
		"added":  true,
		"source": saved,
	})
}

func (s *Server) toolRemoveSource(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		ID          string   `json:"id"`
		IDs         []string `json:"ids"`
		Confirm     bool     `json:"confirm"`
		DeleteFiles bool     `json:"delete_files"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if !args.Confirm {
		return toolError("removing a database source requires confirm=true")
	}
	targets := args.IDs
	if id := strings.TrimSpace(args.ID); id != "" {
		targets = append([]string{id}, targets...)
	}
	if len(targets) == 0 {
		return toolError("either id or ids must be provided")
	}
	removed := make([]string, 0, len(targets))
	dirsDeleted := 0
	for _, rawID := range targets {
		cleanID := strings.ToLower(strings.TrimSpace(rawID))
		if cleanID == "" {
			continue
		}
		if err := dbsync.RemoveSource(s.sourcesPath, cleanID); err == nil {
			removed = append(removed, cleanID)
		}
		if args.DeleteFiles && s.databasesDir != "" {
			if dbsync.DeletePackDir(s.databasesDir, cleanID) == nil {
				dirsDeleted++
			}
		}
	}
	if len(removed) == 0 && dirsDeleted == 0 {
		return toolError("no matching sources or directories found")
	}
	return jsonResult(map[string]any{
		"removed":       true,
		"ids":           removed,
		"deleted_files": dirsDeleted > 0,
		"dirs_deleted":  dirsDeleted,
	})
}

func (s *Server) toolDeletePackFiles(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		Pack         string   `json:"pack"`
		Names        []string `json:"names"`
		IgnoreOnSync *bool    `json:"ignore_on_sync"`
		Confirm      bool     `json:"confirm"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if !args.Confirm {
		return toolError("deleting pack files requires confirm=true")
	}
	if len(args.Names) == 0 {
		return toolError("names must not be empty")
	}
	ignoreOnSync := true
	if args.IgnoreOnSync != nil {
		ignoreOnSync = *args.IgnoreOnSync
	}
	deleted := make([]string, 0, len(args.Names))
	var packDir string
	for _, name := range args.Names {
		ref, err := s.resolvePackEntry(args.Pack, name)
		if err != nil {
			continue
		}
		if err := s.store.DeleteDatabaseEntry(ref); err != nil {
			return toolError(err.Error())
		}
		deleted = append(deleted, ref.Name)
		packDir = filepath.Dir(ref.Path)
	}
	if ignoreOnSync && len(deleted) > 0 && packDir != "" {
		_ = dbsync.MarkIgnored(packDir, deleted)
	}
	return jsonResult(map[string]any{
		"deleted":        len(deleted),
		"files":          deleted,
		"ignored_on_sync": ignoreOnSync,
	})
}

func (s *Server) toolSyncSources(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		ID string `json:"id"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	list, err := dbsync.LoadSources(s.sourcesPath)
	if err != nil {
		return toolError(err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), mcpSyncTimeout)
	defer cancel()

	if id := strings.ToLower(strings.TrimSpace(args.ID)); id != "" {
		for _, src := range list {
			if src.ID == id {
				rep := s.syncer.SyncSource(ctx, src, nil)
				return jsonResult(map[string]any{"reports": []dbsync.SyncReport{rep}})
			}
		}
		return toolError(fmt.Sprintf("source %q not found", id))
	}
	reports := s.syncer.SyncAll(ctx, list, nil)
	return jsonResult(map[string]any{
		"count":   len(reports),
		"reports": reports,
	})
}
