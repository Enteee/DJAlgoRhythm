package i18n

import (
	"sort"
	"testing"
)

// TestI18nCompleteness verifies that all language profiles contain all message keys
func TestI18nCompleteness(t *testing.T) {
	// Get all supported languages
	languages := GetSupportedLanguages()
	if len(languages) == 0 {
		t.Fatal("No supported languages found")
	}

	// Get the reference messages from English (assumed to be complete)
	referenceMessages := getMessages(DefaultLanguage)
	if len(referenceMessages) == 0 {
		t.Fatal("No reference messages found in default language")
	}

	// Extract all keys from reference messages
	var referenceKeys []string
	for key := range referenceMessages {
		referenceKeys = append(referenceKeys, key)
	}
	sort.Strings(referenceKeys)

	t.Logf("Reference language (%s) has %d message keys", DefaultLanguage, len(referenceKeys))

	// Test each language profile
	for _, lang := range languages {
		t.Run("Language_"+lang, func(t *testing.T) {
			messages := getMessages(lang)

			// Get keys from this language
			var langKeys []string
			for key := range messages {
				langKeys = append(langKeys, key)
			}
			sort.Strings(langKeys)

			t.Logf("Language %s has %d message keys", lang, len(langKeys))

			// Check for missing keys
			var missingKeys []string
			for _, refKey := range referenceKeys {
				if _, exists := messages[refKey]; !exists {
					missingKeys = append(missingKeys, refKey)
				}
			}

			// Check for extra keys (keys in this language but not in reference)
			var extraKeys []string
			for _, langKey := range langKeys {
				if _, exists := referenceMessages[langKey]; !exists {
					extraKeys = append(extraKeys, langKey)
				}
			}

			// Report results
			if len(missingKeys) > 0 {
				t.Errorf("Language %s is missing %d keys: %v", lang, len(missingKeys), missingKeys)
			}

			if len(extraKeys) > 0 {
				t.Logf("Language %s has %d extra keys (not in reference): %v", lang, len(extraKeys), extraKeys)
			}

			if len(missingKeys) == 0 && len(extraKeys) == 0 {
				t.Logf("✅ Language %s is complete and matches reference", lang)
			}
		})
	}
}

// TestI18nKeyConsistency verifies that all message keys follow expected patterns
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
			t.Errorf("Message key '%s' does not follow expected naming convention (should start with one of: %v)", key, expectedPrefixes)
		}
	}
}

// TestI18nMessageValues verifies that messages contain expected placeholders
func TestI18nMessageValues(t *testing.T) {
	referenceMessages := getMessages(DefaultLanguage)

	// Test specific keys that should have placeholders
	testsWithPlaceholders := map[string]int{
		"prompt.enhanced_approval":         5, // %s - %s, album, year, url
		"prompt.basic_approval":            4, // %s - %s, album, year
		"admin.approval_prompt":            3, // user, song, link
		"success.track_added":              3, // artist, title, playlist
		"success.admin_approved_and_added": 3, // artist, title, url
		"format.album":                     1, // album name
		"format.year":                      1, // year number
		"format.url":                       1, // url
	}

	for key, expectedPlaceholders := range testsWithPlaceholders {
		if message, exists := referenceMessages[key]; exists {
			placeholderCount := 0
			// Count %s and %d placeholders
			for i := 0; i < len(message)-1; i++ {
				if message[i] == '%' && (message[i+1] == 's' || message[i+1] == 'd') {
					placeholderCount++
				}
			}

			if placeholderCount != expectedPlaceholders {
				t.Errorf("Message key '%s' should have %d placeholders but has %d: %s",
					key, expectedPlaceholders, placeholderCount, message)
			}
		} else {
			t.Errorf("Expected message key '%s' not found", key)
		}
	}
}

// TestLocalizerFunctionality tests the Localizer methods
func TestLocalizerFunctionality(t *testing.T) {
	// Test English localizer
	localizer := NewLocalizer(DefaultLanguage)
	if localizer == nil {
		t.Fatal("Failed to create localizer")
	}

	// Test existing key
	result := localizer.T("error.generic")
	if result == "" || result == "error.generic" {
		t.Errorf("Expected translated message for 'error.generic', got: %s", result)
	}

	// Test non-existing key (should return the key itself)
	nonExistentKey := "this.key.does.not.exist"
	result = localizer.T(nonExistentKey)
	if result != nonExistentKey {
		t.Errorf("Expected fallback to key name for non-existent key, got: %s", result)
	}

	// Test message with parameters
	result = localizer.T("format.album", "Test Album")
	expected := " (Album: Test Album)"
	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}

	// Test fallback to English for non-English language missing key
	if len(GetSupportedLanguages()) > 1 {
		// Find a non-English language
		var nonEnglishLang string
		for _, lang := range GetSupportedLanguages() {
			if lang != DefaultLanguage {
				nonEnglishLang = lang
				break
			}
		}

		if nonEnglishLang != "" {
			nonEnglishLocalizer := NewLocalizer(nonEnglishLang)
			// Test with a key that should exist in English
			fallbackResult := nonEnglishLocalizer.T("error.generic")
			if fallbackResult == "" || fallbackResult == "error.generic" {
				t.Errorf("Expected fallback to English for non-English localizer, got: %s", fallbackResult)
			}
		}
	}
}

// TestGetSupportedLanguages verifies the supported languages function
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

// BenchmarkLocalizer benchmarks the localization performance
func BenchmarkLocalizer(b *testing.B) {
	localizer := NewLocalizer(DefaultLanguage)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = localizer.T("error.generic")
	}
}

// BenchmarkLocalizerWithArgs benchmarks localization with arguments
func BenchmarkLocalizerWithArgs(b *testing.B) {
	localizer := NewLocalizer(DefaultLanguage)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = localizer.T("format.album", "Test Album Name")
	}
}
