package llm

import (
	"context"
	"strings"

	"go.uber.org/zap"
)

// IsChatterMessage determines if a message is likely chatter rather than a music request.
// This is a conservative approach that only filters out obvious non-music content.
func IsChatterMessage(_ context.Context, text string, logger *zap.Logger) (bool, error) {
	logger.Debug("Chatter detection (basic implementation)",
		zap.String("text", text))

	// Filter out obvious non-music chatter
	chatterKeywords := []string{
		"hello", "hi", "hey", "good morning", "good afternoon", "good evening", "good night",
		"how are you", "how's everyone", "what's up", "weather", "lunch", "dinner", "work",
		"tired", "busy", "weekend", "holiday", "birthday", "thanks", "thank you", "lol",
		"haha", "see you", "bye", "goodbye", "later",
	}

	textLower := strings.ToLower(text)

	// Check for obvious chatter patterns
	for _, keyword := range chatterKeywords {
		if strings.Contains(textLower, keyword) {
			return true, nil
		}
	}

	const minMessageLength = 3
	// If very short and no music indicators, likely chatter
	if len(strings.TrimSpace(text)) < minMessageLength {
		return true, nil
	}

	// Default to false (let through) to avoid filtering music requests
	return false, nil
}
