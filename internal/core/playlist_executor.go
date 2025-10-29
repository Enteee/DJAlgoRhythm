package core

import (
	"context"

	"go.uber.org/zap"
)

// Playlist Snapshot Loading
// This module handles loading existing playlist tracks for deduplication

// loadPlaylistSnapshot loads existing tracks from the playlist.
func (d *Dispatcher) loadPlaylistSnapshot(ctx context.Context) error {
	tracks, err := d.spotify.GetPlaylistTracksWithDetails(ctx, d.config.Spotify.PlaylistID)
	if err != nil {
		return err
	}

	// Extract track IDs for dedup store
	trackIDs := make([]string, len(tracks))
	for i, track := range tracks {
		trackIDs[i] = track.ID
	}

	d.dedup.Load(trackIDs)
	d.logger.Info("Loaded playlist snapshot", zap.Int("tracks", len(trackIDs)))
	return nil
}
