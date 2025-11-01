// Package musiclink provides multi-provider music link resolution for converting links to Spotify tracks.
package musiclink

import (
	"context"
)

// TrackInfo holds extracted track information from various music providers.
type TrackInfo struct {
	Title  string // Track title.
	Artist string // Artist name(s).
	ISRC   string // International Standard Recording Code (if available).
}

// Resolver defines the interface for resolving music links from various providers to track information.
type Resolver interface {
	// Resolve extracts track information from a music provider URL.
	Resolve(ctx context.Context, url string) (*TrackInfo, error)

	// CanResolve checks if this resolver can handle the given URL.
	CanResolve(url string) bool
}
