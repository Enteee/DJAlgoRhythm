package core

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"djalgorhythm/internal/chat"
)

// Message Processing and LLM Integration
// This module handles the core message processing logic including LLM disambiguation,
// Spotify matching, and user clarification workflows

// askWhichSong asks for clarification on non-Spotify links.
func (d *Dispatcher) askWhichSong(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message) {
	msgCtx.State = StateAskWhichSong

	message := d.formatMessageWithMention(originalMsg, d.localizer.T("prompt.which_song"))
	_, err := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID, message)
	if err != nil {
		d.logger.Error("Failed to ask which song", zap.Error(err))
	}
}

// llmDisambiguate uses enhanced four-stage LLM disambiguation with Spotify search.
func (d *Dispatcher) llmDisambiguate(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message) {
	msgCtx.State = StateLLMDisambiguate

	if d.llm == nil {
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.llm.no_provider"))
		return
	}

	// Stage 0: Extract normalized song query from user text
	d.logger.Debug("Stage 0: Extracting song request from message", zap.String("text", msgCtx.Input.Text))

	normalizedQuery := msgCtx.Input.Text
	if d.llm != nil {
		if extractedQuery, err := d.llm.ExtractSongQuery(ctx, msgCtx.Input.Text); err != nil {
			d.logger.Warn("Extraction failed; falling back to raw text", zap.Error(err))
		} else if extractedQuery != "" {
			normalizedQuery = extractedQuery
			d.logger.Info("Stage 0 complete: Using normalized query",
				zap.String("original_text", msgCtx.Input.Text),
				zap.String("normalized_query", normalizedQuery))
		} else {
			d.logger.Debug("Stage 0: Empty extraction result; using raw text")
		}
	}

	// Stage 1: Initial Spotify search using normalized query
	d.logger.Debug("Stage 1: Performing initial Spotify search",
		zap.String("original_text", msgCtx.Input.Text),
		zap.String("search_query", normalizedQuery))

	initialSpotifyTracks, err := d.spotify.SearchTrack(ctx, normalizedQuery)
	if err != nil {
		d.logger.Error("Initial Spotify search failed", zap.Error(err))
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.spotify.search_failed"))
		return
	}

	d.logger.Info("Stage 1 complete: Found Spotify tracks",
		zap.Int("count", len(initialSpotifyTracks)))

	// Stage 2: LLM ranking of Spotify results with normalized query
	var rankedTracks []Track

	if len(initialSpotifyTracks) > 0 {
		d.logger.Debug("Stage 2: LLM ranking of Spotify results")
		rankedTracks = d.llm.RankTracks(ctx, normalizedQuery, initialSpotifyTracks)
	} else {
		d.logger.Debug("Stage 2: No initial Spotify results, cannot process without tracks")
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.spotify.no_matches"))
		return
	}

	if len(rankedTracks) == 0 {
		d.logger.Warn("LLM returned no ranked tracks")
		d.askWhichSong(ctx, msgCtx, originalMsg)
		return
	}

	d.logger.Info("Stage 2 complete: LLM ranked Spotify results",
		zap.Int("count", len(rankedTracks)),
		zap.String("top_candidate", fmt.Sprintf("%s - %s",
			rankedTracks[0].Artist, rankedTracks[0].Title)))

	// Stage 3: Enhanced disambiguation with more targeted Spotify search
	d.enhancedLLMDisambiguate(ctx, msgCtx, originalMsg, rankedTracks)
}

// enhancedLLMDisambiguate performs Stage 3: targeted Spotify search and final LLM ranking.
func (d *Dispatcher) enhancedLLMDisambiguate(ctx context.Context, msgCtx *MessageContext,
	originalMsg *chat.Message, rankedTracks []Track) {
	msgCtx.State = StateEnhancedLLMDisambiguate

	allSpotifyTracks := d.performTargetedSpotifySearch(ctx, rankedTracks)
	if len(allSpotifyTracks) == 0 {
		d.logger.Error("No Spotify tracks found for any ranked candidates")
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.spotify.no_matches"))
		return
	}

	finalTracks := d.performFinalLLMRanking(ctx, msgCtx, originalMsg, allSpotifyTracks)
	if len(finalTracks) == 0 {
		return
	}

	d.processFinalTrackSelection(ctx, msgCtx, originalMsg, finalTracks, allSpotifyTracks)
}

// performTargetedSpotifySearch conducts Stage 3a: targeted Spotify search with ranked candidates.
func (d *Dispatcher) performTargetedSpotifySearch(ctx context.Context, rankedTracks []Track) []Track {
	d.logger.Debug("Stage 3a: Targeted Spotify search with ranked candidates")

	const maxRankedCandidates = 3
	var allSpotifyTracks []Track

	for i, track := range rankedTracks {
		if i >= maxRankedCandidates {
			break
		}

		tracks := d.searchSpotifyForLLMCandidate(ctx, &track)
		allSpotifyTracks = append(allSpotifyTracks, tracks...)
	}

	d.logger.Info("Stage 3a complete: Found targeted Spotify tracks",
		zap.Int("count", len(allSpotifyTracks)))

	return allSpotifyTracks
}

// searchSpotifyForLLMCandidate searches Spotify for a specific track and returns top results.
func (d *Dispatcher) searchSpotifyForLLMCandidate(ctx context.Context, track *Track) []Track {
	searchQuery := fmt.Sprintf("%s %s", track.Artist, track.Title)
	d.logger.Debug("Searching Spotify", zap.String("query", searchQuery))

	tracks, err := d.spotify.SearchTrack(ctx, searchQuery)
	if err != nil {
		d.logger.Warn("Spotify search failed for candidate",
			zap.String("query", searchQuery),
			zap.Error(err))
		return nil
	}

	// Take top results from this search
	maxResults := 3
	if len(tracks) < maxResults {
		maxResults = len(tracks)
	}

	return tracks[:maxResults]
}

// performFinalLLMRanking conducts Stage 3b: final LLM ranking of targeted results.
func (d *Dispatcher) performFinalLLMRanking(ctx context.Context, msgCtx *MessageContext,
	originalMsg *chat.Message, allSpotifyTracks []Track) []Track {
	d.logger.Debug("Stage 3b: Final LLM ranking of targeted results")

	finalTracks := d.llm.RankTracks(ctx, msgCtx.Input.Text, allSpotifyTracks)
	if len(finalTracks) == 0 {
		d.logger.Warn("Final LLM returned no tracks, asking which song")
		d.askWhichSong(ctx, msgCtx, originalMsg)
		return nil
	}

	d.logger.Info("Stage 3b complete: Final ranking finished",
		zap.Int("final_tracks", len(finalTracks)),
		zap.String("top_result", fmt.Sprintf("%s - %s",
			finalTracks[0].Artist, finalTracks[0].Title)))

	return finalTracks
}

// processFinalTrackSelection handles the final track selection and approval.
func (d *Dispatcher) processFinalTrackSelection(ctx context.Context, msgCtx *MessageContext,
	originalMsg *chat.Message, finalTracks, allSpotifyTracks []Track) {
	// Match LLM tracks back to original Spotify tracks to restore URLs and IDs
	d.matchSpotifyTrackData(finalTracks, allSpotifyTracks)

	// Store tracks and proceed with user approval
	msgCtx.Candidates = finalTracks
	best := finalTracks[0]

	// Binary decision: if we have a valid Spotify URL, use enhanced approval, otherwise ask which song
	if best.URL != "" {
		d.promptEnhancedApproval(ctx, msgCtx, originalMsg, &best)
	} else {
		d.logger.Warn("Enhanced LLM track missing Spotify URL, asking which song",
			zap.String("artist", best.Artist),
			zap.String("title", best.Title))
		d.askWhichSong(ctx, msgCtx, originalMsg)
	}
}

// matchSpotifyTrackData matches LLM candidates back to original Spotify tracks to restore URLs and IDs.
func (d *Dispatcher) matchSpotifyTrackData(candidates, spotifyTracks []Track) {
	for i := range candidates {
		candidate := &candidates[i]

		// Find best matching Spotify track
		bestMatch := d.findBestSpotifyMatch(candidate, spotifyTracks)
		if bestMatch != nil {
			// Restore complete Spotify data
			candidate.ID = bestMatch.ID
			candidate.URL = bestMatch.URL
			candidate.Duration = bestMatch.Duration
			// Keep LLM's values for other fields as they might be more accurate
		} else {
			d.logger.Warn("Could not match LLM track to Spotify track",
				zap.String("artist", candidate.Artist),
				zap.String("title", candidate.Title))
		}
	}
}

// findBestSpotifyMatch finds the best matching Spotify track for an LLM candidate.
func (d *Dispatcher) findBestSpotifyMatch(track *Track, spotifyTracks []Track) *Track {
	// First try exact match on artist and title
	for i := range spotifyTracks {
		spotifyTrack := &spotifyTracks[i]
		if d.isExactMatch(track, spotifyTrack) {
			return spotifyTrack
		}
	}

	// Then try case-insensitive match
	for i := range spotifyTracks {
		spotifyTrack := &spotifyTracks[i]
		if d.isCaseInsensitiveMatch(track, spotifyTrack) {
			return spotifyTrack
		}
	}

	// Finally try partial match (contains)
	for i := range spotifyTracks {
		spotifyTrack := &spotifyTracks[i]
		if d.isPartialMatch(track, spotifyTrack) {
			return spotifyTrack
		}
	}

	return nil
}

// isExactMatch checks for exact artist and title match.
func (d *Dispatcher) isExactMatch(track1, track2 *Track) bool {
	return track1.Artist == track2.Artist && track1.Title == track2.Title
}

// isCaseInsensitiveMatch checks for case-insensitive artist and title match.
func (d *Dispatcher) isCaseInsensitiveMatch(track1, track2 *Track) bool {
	return strings.EqualFold(track1.Artist, track2.Artist) && strings.EqualFold(track1.Title, track2.Title)
}

// isPartialMatch checks if one track's info is contained in the other.
func (d *Dispatcher) isPartialMatch(track1, track2 *Track) bool {
	artist1 := strings.ToLower(track1.Artist)
	title1 := strings.ToLower(track1.Title)
	artist2 := strings.ToLower(track2.Artist)
	title2 := strings.ToLower(track2.Title)

	// Check if artist and title contain each other (for variations)
	return (strings.Contains(artist1, artist2) || strings.Contains(artist2, artist1)) &&
		(strings.Contains(title1, title2) || strings.Contains(title2, title1))
}
