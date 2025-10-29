package i18n

import (
	"sort"
	"testing"
)

const testErrorGenericKey = "error.generic"

// TestI18nCompleteness verifies that all language profiles contain all message keys.
func TestI18nCompleteness(t *testing.T) {
	languages := validateLanguagesExist(t)
	referenceMessages := validateReferenceMessages(t)
	referenceKeys := extractAndSortKeys(referenceMessages)

	t.Logf("Reference language (%s) has %d message keys", DefaultLanguage, len(referenceKeys))

	// Test each language profile
	for _, lang := range languages {
		t.Run("Language_"+lang, func(t *testing.T) {
			testLanguageCompleteness(t, lang, referenceMessages, referenceKeys)
		})
	}
}

// validateLanguagesExist checks that supported languages exist and returns them.
func validateLanguagesExist(t *testing.T) []string {
	t.Helper()
	languages := GetSupportedLanguages()
	if len(languages) == 0 {
		t.Fatal("No supported languages found")
	}
	return languages
}

// validateReferenceMessages gets and validates reference messages.
func validateReferenceMessages(t *testing.T) map[string]string {
	t.Helper()
	referenceMessages := getMessages(DefaultLanguage)
	if len(referenceMessages) == 0 {
		t.Fatal("No reference messages found in default language")
	}
	return referenceMessages
}

// extractAndSortKeys extracts all keys from messages and sorts them.
func extractAndSortKeys(messages map[string]string) []string {
	keys := make([]string, 0, len(messages))
	for key := range messages {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// testLanguageCompleteness tests a single language for completeness.
func testLanguageCompleteness(t *testing.T, lang string, referenceMessages map[string]string, referenceKeys []string) {
	t.Helper()
	messages := getMessages(lang)
	langKeys := extractAndSortKeys(messages)

	t.Logf("Language %s has %d message keys", lang, len(langKeys))

	missingKeys := findMissingKeys(messages, referenceKeys)
	extraKeys := findExtraKeys(referenceMessages, langKeys)

	reportCompletenessResults(t, lang, missingKeys, extraKeys)
}

// findMissingKeys finds keys that are in reference but not in the target messages.
func findMissingKeys(messages map[string]string, referenceKeys []string) []string {
	var missingKeys []string
	for _, refKey := range referenceKeys {
		if _, exists := messages[refKey]; !exists {
			missingKeys = append(missingKeys, refKey)
		}
	}
	return missingKeys
}

// findExtraKeys finds keys that are in target but not in reference messages.
func findExtraKeys(referenceMessages map[string]string, targetKeys []string) []string {
	var extraKeys []string
	for _, key := range targetKeys {
		if _, exists := referenceMessages[key]; !exists {
			extraKeys = append(extraKeys, key)
		}
	}
	return extraKeys
}

// reportCompletenessResults reports the completeness test results.
func reportCompletenessResults(t *testing.T, lang string, missingKeys, extraKeys []string) {
	t.Helper()
	if len(missingKeys) > 0 {
		t.Errorf("Language %s is missing %d keys: %v", lang, len(missingKeys), missingKeys)
	}

	if len(extraKeys) > 0 {
		t.Logf("Language %s has %d extra keys (not in reference): %v", lang, len(extraKeys), extraKeys)
	}

	if len(missingKeys) == 0 && len(extraKeys) == 0 {
		t.Logf("✅ Language %s is complete and matches reference", lang)
	}
}

// TestI18nKeyConsistency verifies that all message keys follow expected patterns.
func TestI18nKeyConsistency(t *testing.T) {
	expectedPrefixes := []string{
		"error.",
		"prompt.",
		"format.",
		"admin.",
		"success.",
		"callback.",
		"button.",
		"bot.",
	}

	referenceMessages := getMessages(DefaultLanguage)

	for key := range referenceMessages {
		hasValidPrefix := false
		for _, prefix := range expectedPrefixes {
			if len(key) > len(prefix) && key[:len(prefix)] == prefix {
				hasValidPrefix = true
				break
			}
		}

		if !hasValidPrefix {
			t.Errorf("Message key '%s' does not follow expected naming convention "+
				"(should start with one of: %v)", key, expectedPrefixes)
		}
	}
}

// TestI18nMessageValues verifies that messages contain expected placeholders.
func TestI18nMessageValues(t *testing.T) {
	referenceMessages := getMessages(DefaultLanguage)

	testsWithPlaceholders := getTestsWithPlaceholders()
	testsWithoutPlaceholders := getTestsWithoutPlaceholders()

	testMessagesWithPlaceholders(t, referenceMessages, testsWithPlaceholders)
	testMessagesWithoutPlaceholders(t, referenceMessages, testsWithoutPlaceholders)
}

// getTestsWithPlaceholders returns the test cases for messages with placeholders.
func getTestsWithPlaceholders() map[string]int {
	return map[string]int{
		"prompt.enhanced_approval":          6, // %s - %s, album, year, url, mood
		"admin.approval_prompt":             4, // user, song, link, mood
		"admin.approval_required_community": 7, // artist, title, album, year, url, mood, threshold
		"success.track_added":               3, // artist, title, playlist
		"success.admin_approved_and_added":  3, // artist, title, url
		"success.track_priority_playing":    3, // artist, title, url
		"bot.startup":                       1, // playlist url
		"bot.queue_management":              5, // artist, title, url, mood, newTrackMood
		"bot.queue_management_auto":         5, // artist, title, url, mood, newTrackMood
		"bot.queue_replacement":             5, // artist, title, url, mood, newTrackMood
		"bot.queue_replacement_auto":        5, // artist, title, url, mood, newTrackMood
		"format.album":                      1, // album name
		"format.year":                       1, // year number
		"format.url":                        1, // url
	}
}

// getTestsWithoutPlaceholders returns the test cases for messages without placeholders.
func getTestsWithoutPlaceholders() []string {
	return []string{
		"admin.no_active_device",         // device notification message
		"admin.insufficient_permissions", // bot permissions notification message
	}
}

// countPlaceholders counts the number of %s and %d placeholders in a message.
func countPlaceholders(message string) int {
	placeholderCount := 0
	for i := range len(message) - 1 {
		if message[i] == '%' && (message[i+1] == 's' || message[i+1] == 'd') {
			placeholderCount++
		}
	}
	return placeholderCount
}

// testMessagesWithPlaceholders tests that messages have the expected number of placeholders.
func testMessagesWithPlaceholders(t *testing.T, referenceMessages map[string]string, tests map[string]int) {
	t.Helper()
	for key, expectedPlaceholders := range tests {
		message, exists := referenceMessages[key]
		if !exists {
			t.Errorf("Expected message key '%s' not found", key)
			continue
		}

		placeholderCount := countPlaceholders(message)
		if placeholderCount != expectedPlaceholders {
			t.Errorf("Message key '%s' should have %d placeholders but has %d: %s",
				key, expectedPlaceholders, placeholderCount, message)
		}
	}
}

// testMessagesWithoutPlaceholders tests that messages have no placeholders.
func testMessagesWithoutPlaceholders(t *testing.T, referenceMessages map[string]string, tests []string) {
	t.Helper()
	for _, key := range tests {
		message, exists := referenceMessages[key]
		if !exists {
			t.Errorf("Expected message key '%s' not found", key)
			continue
		}

		placeholderCount := countPlaceholders(message)
		if placeholderCount > 0 {
			t.Errorf("Message key '%s' should have no placeholders but has %d: %s",
				key, placeholderCount, message)
		}
	}
}

// TestLocalizerFunctionality tests the Localizer methods.
func TestLocalizerFunctionality(t *testing.T) {
	localizer := createLocalizerOrFail(t)

	testExistingKey(t, localizer)
	testNonExistingKey(t, localizer)
	testMessageWithParameters(t, localizer)
	testFallbackToEnglish(t)
}

// createLocalizerOrFail creates a localizer or fails the test.
func createLocalizerOrFail(t *testing.T) *Localizer {
	t.Helper()
	localizer := NewLocalizer(DefaultLanguage)
	if localizer == nil {
		t.Fatal("Failed to create localizer")
	}
	return localizer
}

// testExistingKey tests translation of an existing key.
func testExistingKey(t *testing.T, localizer *Localizer) {
	t.Helper()
	result := localizer.T(testErrorGenericKey)
	if result == "" || result == testErrorGenericKey {
		t.Errorf("Expected translated message for 'error.generic', got: %s", result)
	}
}

// testNonExistingKey tests that non-existing keys return the key itself.
func testNonExistingKey(t *testing.T, localizer *Localizer) {
	t.Helper()
	nonExistentKey := "this.key.does.not.exist"
	result := localizer.T(nonExistentKey)
	if result != nonExistentKey {
		t.Errorf("Expected fallback to key name for non-existent key, got: %s", result)
	}
}

// testMessageWithParameters tests translation with parameters.
func testMessageWithParameters(t *testing.T, localizer *Localizer) {
	t.Helper()
	result := localizer.T("format.album", "Test Album")
	expected := " (Album: Test Album)"
	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

// testFallbackToEnglish tests fallback to English for non-English languages.
func testFallbackToEnglish(t *testing.T) {
	t.Helper()
	if len(GetSupportedLanguages()) <= 1 {
		return
	}

	nonEnglishLang := findNonEnglishLanguage()
	if nonEnglishLang == "" {
		return
	}

	testNonEnglishFallback(t, nonEnglishLang)
}

// findNonEnglishLanguage finds a non-English language from supported languages.
func findNonEnglishLanguage() string {
	for _, lang := range GetSupportedLanguages() {
		if lang != DefaultLanguage {
			return lang
		}
	}
	return ""
}

// testNonEnglishFallback tests that non-English localizer falls back to English.
func testNonEnglishFallback(t *testing.T, lang string) {
	t.Helper()
	nonEnglishLocalizer := NewLocalizer(lang)
	fallbackResult := nonEnglishLocalizer.T(testErrorGenericKey)
	if fallbackResult == "" || fallbackResult == testErrorGenericKey {
		t.Errorf("Expected fallback to English for non-English localizer, got: %s", fallbackResult)
	}
}

// TestGetSupportedLanguages verifies the supported languages function.
func TestGetSupportedLanguages(t *testing.T) {
	languages := GetSupportedLanguages()

	if len(languages) == 0 {
		t.Error("GetSupportedLanguages should return at least one language")
	}

	// Should include default language
	foundDefault := false
	for _, lang := range languages {
		if lang == DefaultLanguage {
			foundDefault = true
			break
		}
	}

	if !foundDefault {
		t.Errorf("GetSupportedLanguages should include default language '%s'", DefaultLanguage)
	}

	t.Logf("Supported languages: %v", languages)
}

// BenchmarkLocalizer benchmarks the localization performance.
func BenchmarkLocalizer(b *testing.B) {
	localizer := NewLocalizer(DefaultLanguage)

	b.ResetTimer()
	for range b.N {
		_ = localizer.T(testErrorGenericKey)
	}
}

// BenchmarkLocalizerWithArgs benchmarks localization with arguments.
func BenchmarkLocalizerWithArgs(b *testing.B) {
	localizer := NewLocalizer(DefaultLanguage)

	b.ResetTimer()
	for range b.N {
		_ = localizer.T("format.album", "Test Album Name")
	}
}
