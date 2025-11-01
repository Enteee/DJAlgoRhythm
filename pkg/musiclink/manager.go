package musiclink

import (
	"context"
	"errors"
)

// Manager coordinates multiple music link resolvers to handle various provider URLs.
type Manager struct {
	resolvers []Resolver
}

// NewManager creates a new music link manager with all supported resolvers.
func NewManager() *Manager {
	return &Manager{
		resolvers: []Resolver{
			NewYouTubeResolver(),
			NewAppleMusicResolver(),
			NewTidalResolver(),
			NewBeatportResolver(),
			NewAmazonMusicResolver(),
			NewSoundCloudResolver(),
		},
	}
}

// Resolve attempts to resolve a music link using the appropriate resolver.
func (m *Manager) Resolve(ctx context.Context, url string) (*TrackInfo, error) {
	for _, resolver := range m.resolvers {
		if resolver.CanResolve(url) {
			return resolver.Resolve(ctx, url)
		}
	}

	return nil, errors.New("no resolver found for URL")
}

// CanResolve checks if any resolver can handle the given URL.
func (m *Manager) CanResolve(url string) bool {
	for _, resolver := range m.resolvers {
		if resolver.CanResolve(url) {
			return true
		}
	}
	return false
}
