package core

import (
	"testing"

	"djalgorhythm/internal/i18n"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.App.Language != i18n.DefaultLanguage {
		t.Errorf("Expected default language to be %s, got %s", i18n.DefaultLanguage, config.App.Language)
	}

	// Test that other defaults are set correctly
	if config.App.ConfirmTimeoutSecs != DefaultConfirmTimeoutSecs {
		t.Errorf("Expected default timeout %d, got %d", DefaultConfirmTimeoutSecs, config.App.ConfirmTimeoutSecs)
	}

	if config.LLM.Provider != "" {
		t.Errorf("Expected default LLM provider to be empty (requiring explicit configuration), got %s", config.LLM.Provider)
	}

	if config.Telegram.Enabled != true {
		t.Errorf("Expected Telegram to be enabled by default")
	}
}

func TestLanguageConfiguration(t *testing.T) {
	config := DefaultConfig()

	// Test supported languages
	supportedLanguages := i18n.GetSupportedLanguages()
	for _, lang := range supportedLanguages {
		config.App.Language = lang
		// Should not panic or error
		localizer := i18n.NewLocalizer(config.App.Language)
		if localizer == nil {
			t.Errorf("Failed to create localizer for language %s", lang)
		}

		// Test a known message key
		message := localizer.T("error.generic")
		if message == "" {
			t.Errorf("Empty message for key 'error.generic' in language %s", lang)
		}
	}
}

func TestConfigConstants(t *testing.T) {
	// Verify configuration constants are reasonable
	if DefaultConfirmTimeoutSecs <= 0 {
		t.Error("DefaultConfirmTimeoutSecs should be positive")
	}

	if DefaultConfirmAdminTimeoutSecs <= DefaultConfirmTimeoutSecs {
		t.Error("Admin timeout should be longer than regular timeout")
	}

	if DefaultServerPort <= 0 || DefaultServerPort > 65535 {
		t.Error("DefaultServerPort should be a valid port number")
	}
}
