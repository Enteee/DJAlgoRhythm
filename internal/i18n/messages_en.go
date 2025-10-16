package i18n

// englishMessages contains all English translations
var englishMessages = map[string]string{
	// Error messages
	"error.spotify.extract_track_id": "Couldn't extract Spotify track ID from the link",
	"error.llm.no_provider":          "I couldn't guess. Could you send me a spotify link to the song?",
	"error.spotify.search_failed":    "I couldn't search Spotify. Please try again.",
	"error.llm.understand":           "I couldn't understand. Could you be more specific?",
	"error.llm.no_songs":             "I couldn't find any songs. Could you be more specific?",
	"error.spotify.no_matches":       "Couldn't find matching songs on Spotify. Could you be more specific?",
	"error.generic":                  "Something went wrong. Please try again.",
	"error.spotify.not_found":        "Couldn't find on Spotify—mind clarifying?",
	"error.admin.track_info":         "Failed to get track information",
	"error.admin.process_failed":     "Admin approval process failed",
	"error.playlist.add_failed":      "Failed to add track to playlist",

	// Questions and prompts
	"prompt.which_song":        "Which song do you mean by that?",
	"prompt.enhanced_approval": "🎵 Found: **%s - %s**%s%s%s\n\nIs this what you're looking for?",
	"prompt.basic_approval":    "Did you mean **%s - %s**%s%s?",
	"prompt.clarification":     "Did you mean **%s - %s**? If not, please clarify.",

	// Format helpers for prompts
	"format.album": " (Album: %s)",
	"format.year":  " (%d)",
	"format.url":   "\n🔗 %s",

	// Admin approval messages
	"admin.approval_required": "⏳ Admin approval required. Waiting for group admin approval...",
	"admin.approved":          "✅ Admin approved! Adding to playlist...",
	"admin.denied":            "❌ Admin denied the song request.",
	"admin.approval_prompt": "🎵 *Admin Approval Required*\n\n" +
		"User: %s\nSong: %s\nLink: %s\n\n" +
		"Do you approve adding this song to the playlist?",
	"admin.button_approve": "✅ Approve",
	"admin.button_deny":    "❌ Deny",

	// Success messages
	"success.track_added": "Added: %s - %s (%s)",
	"success.duplicate":   "Already in playlist.",

	// Callback messages
	"callback.approved":           "✅ Song approved by admin",
	"callback.denied":             "❌ Song denied by admin",
	"callback.already_decided":    "This approval request has already been decided.",
	"callback.not_admin":          "Only group administrators can approve songs.",
	"callback.approval_not_found": "Approval request not found or expired.",
	"callback.expired":            "This approval request has expired.",
	"callback.unauthorized":       "Only group administrators can respond to this.",
	"callback.sender_only":        "Only the original sender can respond to this.",
	"callback.prompt_expired":     "This prompt has expired.",

	// Button texts
	"button.confirm":  "👍 Confirm",
	"button.not_this": "👎 Not this",

	// Bot status messages
	"bot.startup":  "🎵 I am now online and ready to add music to your playlist!",
	"bot.shutdown": "🎵 I am going offline. See you later!",
}
