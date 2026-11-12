package mcpserver

import (
	"context"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nxxxsooo/another/internal/herdr"
)

func (s *Server) watches(ctx context.Context) (herdr.WatchStatus, error) {
	if s.WatchStatus != nil {
		return s.WatchStatus(ctx)
	}
	return (herdr.Client{}).Status(ctx)
}

func (s *Server) watchGranted(ctx context.Context, provider, id string) bool {
	if provider != "opencode" {
		return false
	}
	status, err := s.watches(ctx)
	if err != nil {
		return false
	}
	for supervisor, members := range status.Groups {
		if supervisor == id || slices.Contains(members, id) {
			return true
		}
	}
	return false
}

type watchMember struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	State    string `json:"state"`
	Folder   string `json:"folder"`
	Provider string `json:"provider"`
}

type watchOutput struct {
	Groups   map[string][]watchMember `json:"groups"`
	Warnings []string                 `json:"warnings"`
}

func (s *Server) watchTool(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, watchOutput, error) {
	status, err := s.watches(ctx)
	output := watchOutput{Groups: map[string][]watchMember{}}
	output.Warnings = status.Warnings
	if err != nil {
		return nil, output, err
	}
	for supervisor, members := range status.Groups {
		for _, id := range members {
			live := status.Live[id]
			member := watchMember{ID: id, Provider: "opencode", Title: live.Title, State: live.State}
			if member.State == "" {
				member.State = "offline"
			}
			sm, err := s.Index.Get("opencode", id)
			if err != nil {
				return nil, output, err
			}
			if sm != nil {
				member.Folder = sm.ProjectPath
				if member.Title == "" {
					member.Title = sm.Title
				}
			}
			output.Groups[supervisor] = append(output.Groups[supervisor], member)
		}
	}
	return nil, output, nil
}
