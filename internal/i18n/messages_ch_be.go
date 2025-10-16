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
	"error.admin.track_info":         "Track-Informatione chöi nid ufgnoh wärde.",
	"error.admin.process_failed":     "D Admin-Freigab het nid funktioniert.",
	"error.playlist.add_failed":      "Ha's Lied nid chönne zur Playliste hinzuefüege.",

	// Questions and prompts
	"prompt.which_song":        "Weles Lied meinsch de gnau?",
	"prompt.enhanced_approval": "🎵 Gfunde: **%s - %s**%s%s%s\n\nIsch das z'richtige?",
	"prompt.basic_approval":    "Meinsch **%s - %s**%s%s?",
	"prompt.clarification":     "Meinsch **%s - %s**? Wenn nid, chasch das bitte gnauer erlüterä?",

	// Format helpers for prompts
	"format.album": " (Album: %s)",
	"format.year":  " (%d)",
	"format.url":   "\n🔗 %s",

	// Admin approval messages
	"admin.approval_required": "⏳ Admin-Freigab nötig. Wart bis dr Gruppen-Admin zueseit...",
	"admin.approved":          "✅ Admin hets guet geheisse! Wird zur Playlist zuegfüegt...",
	"admin.denied":            "❌ Admin het z'Lied abglehnt.",
	"admin.approval_prompt":   "🎵 *Admin-Freigab nötig*\n\nUser: %s\nLied: %s\nLink: %s\n\nWottsch das Lied zur Playlist hinzuefüege?",
	"admin.button_approve":    "✅ Isch ok",
	"admin.button_deny":       "❌ Ablehnä",

	// Success messages
	"success.track_added": "Hinzuegfüegt: %s - %s (%s)",
	"success.duplicate":   "Isch scho i dr Playlist.",

	// Callback messages
	"callback.approved":           "✅ Lied isch vom Admin guet geheisse worde.",
	"callback.denied":             "❌ Lied isch vom Admin abglehnt worde.",
	"callback.already_decided":    "Über die Freigab isch scho entschide worde.",
	"callback.not_admin":          "Nur Gruppen-Admins chöi Lieder freigäh.",
	"callback.approval_not_found": "D Freigab-Afroge isch nid gfunde worde oder abgloffe.",
	"callback.expired":            "D Freigab-Afroge isch abgloffe.",
	"callback.unauthorized":       "Nur Gruppen-Admins chöi do druf antworte.",
	"callback.sender_only":        "Nur dä, wo s Lied gschickt het, cha da antworte.",
	"callback.prompt_expired":     "Die Afroge isch abgloffe.",

	// Button texts
	"button.confirm":  "👍 Ja, das isch's",
	"button.not_this": "👎 Nö, nid das",
}
