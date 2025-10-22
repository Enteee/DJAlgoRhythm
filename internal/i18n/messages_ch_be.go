package i18n

// berneseGermanMessages contains all Bernese Swiss German (Bärndütsch) translations
var berneseGermanMessages = map[string]string{
	// Error messages
	"error.spotify.extract_track_id": "Ha d Spotify-Track-ID nid chönne us em Link useläse.",
	"error.llm.no_provider":          "Ha's nid chönne errate. Chasch mir äch e Spotify-Link vom Lied schicke?",
	"error.spotify.search_failed":    "Ha das nid chönne uf Spotify sueche. Bitte probiers nomau.",
	"error.llm.understand":           "Ha di nid ganz verstandä. Chasch es bitzeli konkreter si?",
	"error.llm.no_songs":             "Ha kei Lieder gfunde. Chasch mir meh verzeuä?",
	"error.spotify.no_matches":       "Ha kei passendi Lieder uf Spotify gfunde. Chasch es bitzeli genauer si?",
	"error.generic":                  "Öppis isch schief gloffe. Probier's haut nomau, bitte.",
	"error.spotify.not_found":        "Ha's uf Spotify nid gfunde – chasch das no chli erlüterä?",
	"error.admin.process_failed":     "D Admin-Freigab het nid funktioniert.",
	"error.playlist.add_failed":      "Ha's Lied nid chönne zur Playliste hinzuefüege.",

	// Questions and prompts
	"prompt.which_song":        "Weles Lied meinsch de gnau?",
	"prompt.enhanced_approval": "🎵 Gfunde: %s - %s%s%s%s\n\nIsch das z'richtige?",
	"prompt.basic_approval":    "Meinsch %s - %s%s%s?",
	"prompt.clarification":     "Meinsch %s - %s? Wenn nid, chasch das bitte gnauer erlüterä?",

	// Format helpers for prompts
	"format.album": " (Album: %s)",
	"format.year":  " (%d)",
	"format.url":   "\n🔗 %s",

	// Admin approval messages
	"admin.approval_required_community": "⏳ Admin-Freigab nötig\n\n🎵 %s - %s%s%s%s\n\n" +
		"Wart uf Admin-Freigab oder reagier mit 👍 we das o guet fingsch (%d+ Reaktione für Community-Freigab nötig).",
	"admin.denied": "❌ Admin het z'Lied abglehnt.",
	"admin.approval_prompt": "🎵 *Admin-Freigab nötig*\n\nUser: %s\nLied: %s\nLink: %s\n\n" +
		"Wottsch das Lied zur Playlist hinzuefüege?",
	"admin.button_approve": "✅ Isch ok",
	"admin.button_deny":    "❌ Ablehnä",

	// Success messages
	"success.track_added":                        "Hinzuegfüegt: %s - %s (%s)",
	"success.track_added_with_queue":             "Hinzuegfüegt: %s - %s (%s) - Warteschlange-Position: %d",
	"success.admin_approved_and_added":           "✅ Admin hets guetgeheisse und hinzuegfüegt: %s - %s (%s)",
	"success.admin_approved_and_added_queue":     "✅ Admin hets guetgeheisse und hinzuegfüegt: %s - %s (%s) - Warteschlange-Position: %d",
	"success.community_approved_and_added":       "✅ Community hets guetgeheisse und hinzuegfüegt: %s - %s (%s)",
	"success.community_approved_and_added_queue": "✅ Community hets guetgeheisse und hinzuegfüegt: %s - %s (%s) - Warteschlange-Position: %d",
	"success.track_priority_playing":             "🚀 Spielt jetzt: %s - %s (%s)",
	"success.duplicate":                          "Isch scho i dr Playlist.",

	// Callback messages
	"callback.approved":       "✅ Lied isch vom Admin guet geheisse worde.",
	"callback.denied":         "❌ Lied isch vom Admin abglehnt worde.",
	"callback.expired":        "D Freigab-Afrag isch abgloffe.",
	"callback.unauthorized":   "Nur Gruppe-Admins chöi do druf antworte.",
	"callback.sender_only":    "Nur dä, wo s Lied gschickt het, cha da antworte.",
	"callback.prompt_expired": "Die Afrag isch abgloffe.",

	// Button texts
	"button.confirm":  "👍 Ja, das isch's",
	"button.not_this": "👎 Nö, nid das",

	// Bot status messages
	"bot.startup":  "🎵 Ig bi jetzt online und bereit för öii Musigwünsch!\n\n📀 Playlist: %s",
	"bot.shutdown": "🎵 Ig ga offline. Bis spöter!\n\n📀 Aui Lieder vo dere Session: %s",

	// Queue management messages
	"bot.queue_management": "🤖 D Playlist wird chlii läär! Hinzuegfüegt: %s - %s\n%s\n\n" +
		"💭 Bitte füegt meh Lieder hinzu dass d Musig witer geit!",
	"bot.queue_management_auto": "🤖 D Playlist wird chlii läär! Automatisch hinzuegfüegt: %s - %s\n%s\n\n" +
		"✅ Automatisch hinzuegfüegt nach mehrere Ablehnige.",
	"bot.queue_replacement":      "🔄 Ersatz-Track vorgschlage: %s - %s\n%s\n\n💭 Findsch das guet?",
	"bot.queue_replacement_auto": "🔄 Automatisch Ersatz hinzuegfüegt: %s - %s\n%s\n\n✅ Automatisch hinzuegfüegt nach mehrere Ablehnige.",

	// Playlist monitoring messages
	"bot.shuffle_warning": "⚠️ Warnig: Shuffle isch igschalte!\n\n" +
		"🔀 Bitte schalt Shuffle us für optimals Auto-DJing. " +
		"Shuffle stört d Track-Reihefolg und s Queueing.",
	"bot.repeat_warning": "⚠️ Warnig: Repeat isch uf Track gstellt!\n\n" +
		"🔁 Bitte ändere d Repeat-Modus uf 'us' oder 'Playlist' fürs Auto-DJing. " +
		"Track-Repeat verhinderet Playlist-Fortschritt.",

	// Queue track approval messages
	"button.queue_approve":            "✅ Isch ok",
	"button.queue_deny":               "❌ Ou nei",
	"callback.queue_approved":         "✅ Queue-Track isch guetgeheisse worde",
	"callback.queue_denied":           "❌ Queue-Track isch abglehnt worde",
	"bot.queue_whatsapp_instructions": "💡 Antworte mit 'approve' oder 'deny' zum reagierä.",

	// Device notifications
	"admin.no_active_device": "🔇 Kei aktivi Spotify-Grät gfunde!\n\n" +
		"💡 Mach Spotify uf und fang a spile vo irgendere Playlist zum es Gerät z'aktiviere.",

	// Bot permissions notifications
	"admin.insufficient_permissions": "🔐 Bot-Admin-Berechtigunge nötig!\n\n" +
		"Dr Bot brucht Administrator-Berechtigunge i dr Gruppe zum richtig z'funktioniere.\n\n" +
		"Bitte:\n" +
		"• Mach dr Bot zum Administrator i dr Gruppe\n" +
		"• Gwüssi Bot-Features funktioniered nur mit Admin-Status\n\n" +
		"💡 Admin-Berechtigunge ermögliched em Bot Events z'empfange und Gruppe-Interaktione z'verwalte.",

	// Queue sync notifications
	"admin.queue_sync_warning": "🚨 Queue-Sync Problem detected!\n\n" +
		"D Queue isch villicht nid synchron. Tracks i dr Queue:\n%s\n" +
		"💡 Zum fixe: Spiel eine vo dene Tracks i Spotify zum d Queue z'synchronisiere.",
}
