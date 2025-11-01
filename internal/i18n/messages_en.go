package i18n

// englishMessages contains all English translations.
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
	"error.admin.process_failed":     "Admin approval process failed",
	"error.playlist.add_failed":      "Failed to add track to playlist",

	// Questions and prompts
	"prompt.which_song":        "Which song do you mean by that?",
	"prompt.enhanced_approval": "🎵 Found: %s - %s%s%s%s\n\n🎯 Track mood: %s\n\nIs this what you're looking for?",

	// Format helpers for prompts
	"format.album": " (Album: %s)",
	"format.year":  " (%d)",
	"format.url":   "\n🔗 %s",

	// Admin approval messages
	"admin.approval_required_community": "⏳ Admin Approval Required\n\n🎵 %s - %s%s%s%s\n\n🎯 Track mood: %s\n\n" +
		"Waiting for admin approval or react with 👍 below if you like this as well " +
		"(%d+ reactions needed for community approval).",
	"admin.denied": "❌ Admin denied the song request.",
	"admin.approval_prompt": "🎵 *Admin Approval Required*\n\n" +
		"User: %s\nSong: %s\nLink: %s\n\n🎯 Track mood: %s\n\n" +
		"Do you approve adding this song to the playlist?",
	"admin.button_approve": "✅ Approve",
	"admin.button_deny":    "❌ Deny",

	// Success messages
	"success.track_added":                        "Added: %s - %s (%s)",
	"success.track_added_with_queue":             "Added: %s - %s (%s) - Queue position: %d",
	"success.admin_approved_and_added":           "✅ Admin approved and added: %s - %s (%s)",
	"success.admin_approved_and_added_queue":     "✅ Admin approved and added: %s - %s (%s) - Queue position: %d",
	"success.community_approved_and_added":       "✅ Community approved and added: %s - %s (%s)",
	"success.community_approved_and_added_queue": "✅ Community approved and added: %s - %s (%s) - Queue position: %d",
	"success.track_priority_playing":             "🚀 Now playing: %s - %s (%s)",
	"success.duplicate":                          "Already in playlist.",

	// Callback messages
	"callback.approved":       "✅ Song approved by admin",
	"callback.denied":         "❌ Song denied by admin",
	"callback.expired":        "This approval request has expired.",
	"callback.unauthorized":   "Only group administrators can respond to this.",
	"callback.sender_only":    "Only the original sender can respond to this.",
	"callback.prompt_expired": "This prompt has expired.",

	// Button texts
	"button.confirm":  "👍 Confirm",
	"button.not_this": "👎 Not this",

	// Bot status messages
	"bot.startup":  "🎵 I am now online and ready to add music to your playlist!\n\n📀 Playlist: %s",
	"bot.shutdown": "🎵 I am going offline. See you later!\n\n📀 All songs from this session: %s",
	"bot.help_message": "🎵 DJAlgoRhythm Music Bot Help\n\n" +
		"I can help you add songs to the playlist! Here's how:\n\n" +
		"📍 Send Spotify Links:\n" +
		"Just paste a Spotify track link and I'll add it immediately.\n\n" +
		"🔗 Send Other Music Links:\n" +
		"YouTube, Apple Music, etc. - I'll find the matching song on Spotify.\n\n" +
		"✍️ Free Text Requests:\n" +
		"Just write what you want to hear:\n" +
		"• \"Play Arctic Monkeys\"\n" +
		"• \"Add Bohemian Rhapsody by Queen\"\n" +
		"• \"Some chill lofi beats\"\n\n" +
		"⚡ Priority Requests (Admins):\n" +
		"Prefix with \"prio:\" to play next:\n" +
		"• \"prio: Song Name\"\n\n" +
		"👥 Approval System:\n" +
		"Some songs may require admin approval or community votes.\n\n" +
		"Just send your request and I'll take care of the rest! 🎶",

	// Queue management messages
	"bot.queue_management": "🤖 Playlist is running low! Added: %s - %s\n%s\n\n" +
		"💭 Current mood: %s\n🎯 New track mood: %s\n\nPlease add more songs to keep the music going!",
	"bot.queue_management_auto": "🤖 Playlist is running low! Auto-adding: %s - %s\n%s\n\n" +
		"💭 Current mood: %s\n🎯 New track mood: %s\n\n✅ Added automatically after multiple rejections.",
	"bot.queue_replacement": "🔄 Replacement track suggested: %s - %s\n%s\n\n" +
		"💭 Current mood: %s\n🎯 New track mood: %s\n\nDo you approve this replacement?",
	"bot.queue_replacement_auto": "🔄 Auto-adding replacement: %s - %s\n%s\n\n" +
		"💭 Current mood: %s\n🎯 New track mood: %s\n\n✅ Added automatically after multiple rejections.",

	// Playlist monitoring messages
	"bot.shuffle_warning": "⚠️ Warning: Shuffle is enabled!\n\n" +
		"🔀 Please turn off shuffle for optimal auto-DJing. " +
		"Shuffle interferes with track order and queueing.",
	"bot.repeat_warning": "⚠️ Warning: Repeat is set to track!\n\n" +
		"🔁 Please change repeat mode to 'off' or 'playlist' for auto-DJing. " +
		"Track repeat prevents playlist progression.",

	// Queue track approval messages
	"button.queue_approve":    "✅ Approve",
	"button.queue_deny":       "❌ Deny",
	"callback.queue_approved": "✅ Queue track approved",
	"callback.queue_denied":   "❌ Queue track denied",

	// Device notifications
	"admin.no_active_device": "🔇 No active Spotify device found!\n\n" +
		"💡 Open Spotify and start playing from any playlist to activate a device.",

	// Bot permissions notifications
	"admin.insufficient_permissions": "🔐 Bot Admin Permissions Required!\n\n" +
		"The bot needs administrator privileges in the group to function properly.\n\n" +
		"Please:\n" +
		"• Make the bot an administrator in the group\n" +
		"• Some bot features require admin status to work correctly\n\n" +
		"💡 Admin permissions enable the bot to receive events and manage group interactions.",

	// Queue sync notifications
	"admin.queue_sync_warning": "🚨 Queue Sync Issue Detected!\n\n" +
		"The queue may be out of sync. Queued tracks:\n%s\n" +
		"💡 To fix: Play any of the above tracks in Spotify to resync the queue.",
}
