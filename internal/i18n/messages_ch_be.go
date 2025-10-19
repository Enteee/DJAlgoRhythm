package i18n

// berneseGermanMessages contains all Bernese Swiss German (Bärndütsch) translations
var berneseGermanMessages = map[string]string{
	// Error messages
	"error.spotify.extract_track_id": "Ha d Spotify-Track-ID nid chönne us em Link useläse.",
	"error.llm.no_provider":          "Ha's nid chönne errate. Chasch mir äch e Spotify-Link vom Lied schicke?",
	"error.spotify.search_failed":    "Ha das nid chönne uf Spotify sueche. Bitte probiers nomau.",
	"error.llm.understand":           "Ha di nid ganz verstandä. Chasch es bitzeli konkreter sii?",
	"error.llm.no_songs":             "Ha kei Lieder gfunde. Chasch mir meh verzeuä?",
	"error.spotify.no_matches":       "Ha kei passendi Lieder uf Spotify gfunde. Chasch es bitzeli genauer sii?",
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
	"admin.approval_required_enhanced": "⏳ Admin-Freigab nötig\n\n🎵 %s - %s%s%s%s\n\nWart uf Admin-Freigab...",
	"admin.approval_required_community": "⏳ Admin-Freigab nötig\n\n🎵 %s - %s%s%s%s\n\n" +
		"Wart uf Admin-Freigab oder reagier mit 👍 unde we das o guet fingsch (%d+ Reaktione für Community-Freigab nötig).",
	"admin.denied": "❌ Admin het z'Lied abglehnt.",
	"admin.approval_prompt": "🎵 *Admin-Freigab nötig*\n\nUser: %s\nLied: %s\nLink: %s\n\n" +
		"Wottsch das Lied zur Playlist hinzuefüege?",
	"admin.button_approve": "✅ Isch ok",
	"admin.button_deny":    "❌ Ablehnä",

	// Success messages
	"success.track_added":                    "Hinzuegfüegt: %s - %s (%s)",
	"success.track_added_with_queue":         "Hinzuegfüegt: %s - %s (%s) - Warteschlange-Position: %d",
	"success.admin_approved_and_added":       "✅ Admin hets guetgeheisse und hinzuegfüegt: %s - %s (%s)",
	"success.admin_approved_and_added_queue": "✅ Admin hets guetgeheisse und hinzuegfüegt: %s - %s (%s) - Warteschlange-Position: %d",
	"success.track_priority_playing":         "🚀 Spielt jetzt: %s - %s (%s)",
	"success.duplicate":                      "Isch scho i dr Playlist.",

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

	// Auto-play prevention messages
	"bot.autoplay_prevention": "🤖 D Playlist wird chlii läär! Hinzuegfüegt: %s - %s\n%s\n\n" +
		"💭 Bitte füegt meh Lieder hinzu dass d Musig wiiter geit!",
	"bot.autoplay_replacement":        "🔄 Ersatz-Track vorgeschlage: %s - %s\n%s\n\n💭 Findsch das guet?",
	"bot.autoplay_replacement_failed": "❌ Ha kei Ersatz-Auto-Play-Track gfunde. Bitte füeg selber meh Lieder hinzu!",

	// Playlist monitoring messages
	"bot.playlist_warning": "⚠️ Warnig: Mir spile nid vo de richtige Playlist!\n\n" +
		"🎵 Bitte wächsle zrügg zu dr richtige Playlist: %s\n\n" +
		"💡 Klick uf de Link obe zum schnäu zrügg zu der Playliste z cho.",
	"bot.shuffle_warning": "⚠️ Warnig: Shuffle isch igschalte!\n\n" +
		"🔀 Bitte schalt Shuffle us für optimals Auto-DJing. " +
		"Shuffle stört d Track-Reihefolg und s Queueing.",
	"bot.repeat_warning": "⚠️ Warnig: Repeat isch uf Track gstellt!\n\n" +
		"🔁 Bitte ändere d Repeat-Modus uf 'us' oder 'Playlist' fürs Auto-DJing. " +
		"Track-Repeat verhinderet Playlist-Fortschritt.",
	"bot.playback_compliance_warning": "⚠️ Warnig: Playback-Iistellige müesse agpasst werde!\n\n" +
		"🎵 Ziel-Playlist: %s\n\n" +
		"Bitte prüef dini Spotify-Iistellige:\n" +
		"• Wächsle zu dr richtige Playlist\n" +
		"• Schalt Shuffle us (🔀)\n" +
		"• Stell Repeat uf us oder Playlist (🔁)\n\n" +
		"💡 Die Iistellige sorged für optimals Auto-DJing.",

	// Auto-play approval messages
	"button.autoplay_approve":            "✅ Isch ok",
	"button.autoplay_deny":               "❌ Ou nei",
	"callback.autoplay_approved":         "✅ Auto-Play-Track isch guetgeheisse worde",
	"callback.autoplay_denied":           "❌ Auto-Play-Track isch abglehnt worde",
	"bot.autoplay_whatsapp_instructions": "💡 Antworte mit 'approve' oder 'deny' zum reagierä.",
}
