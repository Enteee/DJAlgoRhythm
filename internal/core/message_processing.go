package core

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"whatdj/internal/chat"
)

// Message Processing and LLM Integration
// This module handles the core message processing logic including LLM disambiguation,
// Spotify matching, and user clarification workflows

// askWhichSong asks for clarification on non-Spotify links
func (d *Dispatcher) askWhichSong(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message) {
	msgCtx.State = StateAskWhichSong

	message := d.formatMessageWithMention(originalMsg, d.localizer.T("prompt.which_song"))
	_, err := d.frontend.SendText(ctx, originalMsg.ChatID, originalMsg.ID, message)
	if err != nil {
		d.logger.Error("Failed to ask which song", zap.Error(err))
	}
}

// llmDisambiguate uses enhanced three-stage LLM disambiguation with Spotify search
func (d *Dispatcher) llmDisambiguate(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message) {
	msgCtx.State = StateLLMDisambiguate

	if d.llm == nil {
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.llm.no_provider"))
		return
	}

	// Stage 1: Initial Spotify search using user input directly
	d.logger.Debug("Stage 1: Performing initial Spotify search",
		zap.String("text", msgCtx.Input.Text))

	initialSpotifyTracks, err := d.spotify.SearchTrack(ctx, msgCtx.Input.Text)
	if err != nil {
		d.logger.Error("Initial Spotify search failed", zap.Error(err))
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.spotify.search_failed"))
		return
	}

	d.logger.Info("Stage 1 complete: Found Spotify tracks",
		zap.Int("count", len(initialSpotifyTracks)))

	// Stage 2: LLM ranking of Spotify results with user context (or extraction if no results)
	var rankedCandidates []LLMCandidate

	if len(initialSpotifyTracks) > 0 {
		d.logger.Debug("Stage 2: LLM ranking of Spotify results")
		spotifyContext := d.buildSpotifyContextForLLM(initialSpotifyTracks, msgCtx.Input.Text)
		rankedCandidates, err = d.llm.RankCandidates(ctx, spotifyContext)
	} else {
		d.logger.Debug("Stage 2: No initial Spotify results, using LLM extraction")
		rankedCandidates, err = d.llm.RankCandidates(ctx, msgCtx.Input.Text)
	}
	if err != nil {
		d.logger.Error("LLM ranking failed", zap.Error(err))
		if len(initialSpotifyTracks) > 0 {
			// Fallback to Spotify results without LLM ranking
			d.fallbackToSpotifyResults(ctx, msgCtx, originalMsg, initialSpotifyTracks)
		} else {
			d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.llm.understand"))
		}
		return
	}

	if len(rankedCandidates) == 0 {
		d.logger.Warn("LLM returned no ranked candidates")
		if len(initialSpotifyTracks) > 0 {
			d.fallbackToSpotifyResults(ctx, msgCtx, originalMsg, initialSpotifyTracks)
		} else {
			d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.llm.no_songs"))
		}
		return
	}

	d.logger.Info("Stage 2 complete: LLM ranked Spotify results",
		zap.Int("count", len(rankedCandidates)),
		zap.String("top_candidate", fmt.Sprintf("%s - %s",
			rankedCandidates[0].Track.Artist, rankedCandidates[0].Track.Title)))

	// Stage 3: Enhanced disambiguation with more targeted Spotify search
	d.enhancedLLMDisambiguate(ctx, msgCtx, originalMsg, rankedCandidates)
}

// enhancedLLMDisambiguate performs Stage 3: targeted Spotify search and final LLM ranking
func (d *Dispatcher) enhancedLLMDisambiguate(ctx context.Context, msgCtx *MessageContext,
	originalMsg *chat.Message, rankedCandidates []LLMCandidate) {
	msgCtx.State = StateEnhancedLLMDisambiguate

	// Stage 3a: Targeted Spotify search with LLM-ranked candidates
	d.logger.Debug("Stage 3a: Targeted Spotify search with ranked candidates")

	const maxRankedCandidates = 3 // Limit API calls
	var allSpotifyTracks []Track
	for i, candidate := range rankedCandidates {
		if i >= maxRankedCandidates {
			break
		}

		searchQuery := fmt.Sprintf("%s %s", candidate.Track.Artist, candidate.Track.Title)
		d.logger.Debug("Searching Spotify",
			zap.String("query", searchQuery),
			zap.Float64("confidence", candidate.Confidence))

		tracks, err := d.spotify.SearchTrack(ctx, searchQuery)
		if err != nil {
			d.logger.Warn("Spotify search failed for candidate",
				zap.String("query", searchQuery),
				zap.Error(err))
			continue
		}

		// Take top results from this search
		maxResults := 3
		if len(tracks) < maxResults {
			maxResults = len(tracks)
		}

		for j := 0; j < maxResults; j++ {
			allSpotifyTracks = append(allSpotifyTracks, tracks[j])
		}
	}

	if len(allSpotifyTracks) == 0 {
		d.logger.Error("No Spotify tracks found for any ranked candidates")
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.spotify.no_matches"))
		return
	}

	d.logger.Info("Stage 3a complete: Found targeted Spotify tracks",
		zap.Int("count", len(allSpotifyTracks)))

	// Stage 3b: Final LLM ranking of targeted Spotify results
	d.logger.Debug("Stage 3b: Final LLM ranking of targeted results")

	spotifyContext := d.buildSpotifyContextForLLM(allSpotifyTracks, msgCtx.Input.Text)
	finalCandidates, err := d.llm.RankCandidates(ctx, spotifyContext)
	if err != nil {
		d.logger.Error("Final LLM ranking failed", zap.Error(err))
		// Fallback to targeted Spotify search results
		d.fallbackToSpotifyResults(ctx, msgCtx, originalMsg, allSpotifyTracks)
		return
	}

	if len(finalCandidates) == 0 {
		d.logger.Warn("Final LLM returned no candidates, using targeted Spotify results")
		d.fallbackToSpotifyResults(ctx, msgCtx, originalMsg, allSpotifyTracks)
		return
	}

	d.logger.Info("Stage 3b complete: Final ranking finished",
		zap.Int("final_candidates", len(finalCandidates)),
		zap.String("top_result", fmt.Sprintf("%s - %s",
			finalCandidates[0].Track.Artist, finalCandidates[0].Track.Title)))

	// Match LLM candidates back to original Spotify tracks to restore URLs and IDs
	d.matchSpotifyTrackData(finalCandidates, allSpotifyTracks)

	// Store enhanced candidates and proceed with user approval
	msgCtx.Candidates = finalCandidates
	best := finalCandidates[0]

	// Validate that we have a Spotify URL (since we're reranking actual Spotify tracks)
	if best.Track.URL == "" {
		d.logger.Warn("Enhanced LLM candidate missing Spotify URL, asking for clarification",
			zap.String("artist", best.Track.Artist),
			zap.String("title", best.Track.Title))
		d.clarifyAsk(ctx, msgCtx, originalMsg, &best)
		return
	}

	if best.Confidence >= d.config.LLM.Threshold {
		d.promptEnhancedApproval(ctx, msgCtx, originalMsg, &best)
	} else {
		d.clarifyAsk(ctx, msgCtx, originalMsg, &best)
	}
}

// buildSpotifyContextForLLM creates enhanced context for LLM re-ranking
func (d *Dispatcher) buildSpotifyContextForLLM(tracks []Track, originalText string) string {
	context := fmt.Sprintf("User said: %q\n\nAvailable songs from Spotify:\n", originalText)

	for i, track := range tracks {
		context += fmt.Sprintf("%d. %s - %s", i+1, track.Artist, track.Title)
		if track.Album != "" {
			context += fmt.Sprintf(" (Album: %s)", track.Album)
		}
		if track.Year > 0 {
			context += fmt.Sprintf(" (%d)", track.Year)
		}
		context += "\n"
	}

	context += "\nPlease rank these songs based on how well they match what the user is looking for."
	return context
}

// matchSpotifyTrackData matches LLM candidates back to original Spotify tracks to restore URLs and IDs
func (d *Dispatcher) matchSpotifyTrackData(candidates []LLMCandidate, spotifyTracks []Track) {
	for i := range candidates {
		candidate := &candidates[i]

		// Find best matching Spotify track
		bestMatch := d.findBestSpotifyMatch(&candidate.Track, spotifyTracks)
		if bestMatch != nil {
			// Restore complete Spotify data
			candidate.Track.ID = bestMatch.ID
			candidate.Track.URL = bestMatch.URL
			candidate.Track.Duration = bestMatch.Duration
			// Keep LLM's values for other fields as they might be more accurate
		} else {
			d.logger.Warn("Could not match LLM candidate to Spotify track",
				zap.String("artist", candidate.Track.Artist),
				zap.String("title", candidate.Track.Title))
		}
	}
}

// findBestSpotifyMatch finds the best matching Spotify track for an LLM candidate
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

// isExactMatch checks for exact artist and title match
func (d *Dispatcher) isExactMatch(track1, track2 *Track) bool {
	return track1.Artist == track2.Artist && track1.Title == track2.Title
}

// isCaseInsensitiveMatch checks for case-insensitive artist and title match
func (d *Dispatcher) isCaseInsensitiveMatch(track1, track2 *Track) bool {
	return strings.EqualFold(track1.Artist, track2.Artist) && strings.EqualFold(track1.Title, track2.Title)
}

// isPartialMatch checks if one track's info is contained in the other
func (d *Dispatcher) isPartialMatch(track1, track2 *Track) bool {
	artist1 := strings.ToLower(track1.Artist)
	title1 := strings.ToLower(track1.Title)
	artist2 := strings.ToLower(track2.Artist)
	title2 := strings.ToLower(track2.Title)

	// Check if artist and title contain each other (for variations)
	return (strings.Contains(artist1, artist2) || strings.Contains(artist2, artist1)) &&
		(strings.Contains(title1, title2) || strings.Contains(title2, title1))
}

// fallbackToSpotifyResults handles fallback when enhanced LLM fails
func (d *Dispatcher) fallbackToSpotifyResults(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, tracks []Track) {
	const (
		baseConfidence      = 0.7
		confidenceDecrement = 0.1
		minConfidence       = 0.3
	)

	// Convert Spotify tracks to LLM candidates with moderate confidence
	var candidates []LLMCandidate
	for i, track := range tracks {
		confidence := baseConfidence - float64(i)*confidenceDecrement
		if confidence < minConfidence {
			confidence = minConfidence
		}

		candidates = append(candidates, LLMCandidate{
			Track:      track,
			Confidence: confidence,
			Reasoning:  "Spotify search result",
		})
	}

	msgCtx.Candidates = candidates
	best := candidates[0]

	// Use regular approval flow since these are search results
	d.promptApproval(ctx, msgCtx, originalMsg, &best)
}

// clarifyAsk asks for clarification with lower confidence
func (d *Dispatcher) clarifyAsk(ctx context.Context, msgCtx *MessageContext, originalMsg *chat.Message, candidate *LLMCandidate) {
	msgCtx.State = StateClarifyAsk

	prompt := d.localizer.T("prompt.clarification", candidate.Track.Artist, candidate.Track.Title)
	promptWithMention := d.formatMessageWithMention(originalMsg, prompt)

	approved, err := d.frontend.AwaitApproval(ctx, originalMsg, promptWithMention, d.config.App.ConfirmTimeoutSecs)
	if err != nil {
		d.logger.Error("Failed to get clarification", zap.Error(err))
		d.replyError(ctx, msgCtx, originalMsg, d.localizer.T("error.generic"))
		return
	}

	if approved {
		d.handleApproval(ctx, msgCtx, originalMsg)
	} else {
		d.askWhichSong(ctx, msgCtx, originalMsg)
	}
}
