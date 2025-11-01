package core

import (
	"context"

	"djalgorhythm/pkg/musiclink"
)

// musicLinkManagerAdapter adapts pkg/musiclink.ManagerAdapter to core.MusicLinkResolver.
type musicLinkManagerAdapter struct {
	manager *musiclink.ManagerAdapter
}

// NewMusicLinkManagerAdapter creates a new adapter for the music link manager.
func NewMusicLinkManagerAdapter() MusicLinkResolver {
	return &musicLinkManagerAdapter{
		manager: musiclink.NewManagerAdapter(),
	}
}

// Resolve resolves a music link to track information.
func (a *musicLinkManagerAdapter) Resolve(ctx context.Context, url string) (*MusicLinkTrackInfo, error) {
	info, err := a.manager.Resolve(ctx, url)
	if err != nil {
		return nil, err
	}

	return &MusicLinkTrackInfo{
		Title:  info.Title,
		Artist: info.Artist,
		ISRC:   info.ISRC,
	}, nil
}

// CanResolve checks if the manager can resolve the given URL.
func (a *musicLinkManagerAdapter) CanResolve(url string) bool {
	return a.manager.CanResolve(url)
}
