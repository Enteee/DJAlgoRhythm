package musiclink

import (
	"context"
)

// CoreTrackInfo is an interface for the core package's track info type.
// This avoids a circular dependency between pkg/musiclink and internal/core.
type CoreTrackInfo struct {
	Title  string
	Artist string
	ISRC   string
}

// ManagerAdapter adapts the Manager to work with the core package's interface.
type ManagerAdapter struct {
	manager *Manager
}

// NewManagerAdapter creates a new adapter for the music link manager.
func NewManagerAdapter() *ManagerAdapter {
	return &ManagerAdapter{
		manager: NewManager(),
	}
}

// Resolve resolves a music link to track information.
func (a *ManagerAdapter) Resolve(ctx context.Context, url string) (*CoreTrackInfo, error) {
	info, err := a.manager.Resolve(ctx, url)
	if err != nil {
		return nil, err
	}

	return &CoreTrackInfo{
		Title:  info.Title,
		Artist: info.Artist,
		ISRC:   info.ISRC,
	}, nil
}

// CanResolve checks if the manager can resolve the given URL.
func (a *ManagerAdapter) CanResolve(url string) bool {
	return a.manager.CanResolve(url)
}
