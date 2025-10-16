// Package i18n provides internationalization support for user-facing messages
package i18n

import (
	"fmt"
)

const (
	// DefaultLanguage is the fallback language when no translation is available
	DefaultLanguage = "en"
	// BerneseGermanMessages is a Swiss Dialect spoken in the Canton of Bern
	BerneseGermanMessages = "ch_be"
)

// Localizer provides translation functionality
type Localizer struct {
	language string
	messages map[string]string
}

// NewLocalizer creates a new localizer for the specified language
func NewLocalizer(language string) *Localizer {
	return &Localizer{
		language: language,
		messages: getMessages(language),
	}
}

// T translates a message key, with optional parameters for formatting
func (l *Localizer) T(key string, args ...interface{}) string {
	if message, exists := l.messages[key]; exists {
		if len(args) > 0 {
			return fmt.Sprintf(message, args...)
		}
		return message
	}

	// Fallback to English if key not found in current language
	if l.language != DefaultLanguage {
		if fallbackMessage, exists := getMessages(DefaultLanguage)[key]; exists {
			if len(args) > 0 {
				return fmt.Sprintf(fallbackMessage, args...)
			}
			return fallbackMessage
		}
	}

	// Ultimate fallback: return the key itself
	return key
}

// GetSupportedLanguages returns list of supported language codes
func GetSupportedLanguages() []string {
	return []string{DefaultLanguage, BerneseGermanMessages}
}

// getMessages returns the message map for a given language
func getMessages(language string) map[string]string {
	switch language {
	case DefaultLanguage:
		return englishMessages
	case BerneseGermanMessages:
		return berneseGermanMessages
	default:
		return englishMessages // Default to English
	}
}
