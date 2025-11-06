// Package core provides the main business logic and configuration for DJAlgoRhythm.
package core

import (
	"time"

	"djalgorhythm/internal/i18n"
)

// Default configuration values.
const (
	DefaultServerPort                         = 8080
	DefaultTimeoutSeconds                     = 10
	DefaultConfirmTimeoutSecs                 = 120
	DefaultConfirmAdminTimeoutSecs            = 3600
	DefaultQueueTrackApprovalTimeoutSecs      = 30
	DefaultMaxQueueTrackReplacements          = 3
	DefaultQueueAheadDurationSecs             = 90
	DefaultQueueCheckIntervalSecs             = 45
	DefaultShadowQueueMaintenanceIntervalSecs = 30
	DefaultShadowQueueMaxAgeHours             = 2
	DefaultQueueSyncWarningTimeoutMinutes     = 30
	DefaultFloodLimitPerMinute                = 6
)

// Config represents the main application configuration.
type Config struct {
	Telegram TelegramConfig
	Spotify  SpotifyConfig
	LLM      LLMConfig
	Server   ServerConfig
	Log      LogConfig
	App      AppConfig
}

// TelegramConfig holds Telegram bot configuration settings.
type TelegramConfig struct {
	BotToken           string
	GroupID            int64
	AdminApproval      bool
	AdminNeedsApproval bool
	CommunityApproval  int
}

// SpotifyConfig holds Spotify API configuration settings.
type SpotifyConfig struct {
	ClientID      string
	ClientSecret  string
	RedirectURL   string
	OAuthBindHost string // Host to bind OAuth callback server (defaults to Server.Host)
	PlaylistID    string
	TokenPath     string
}

// LLMConfig holds LLM provider configuration settings.
type LLMConfig struct {
	Provider string
	Model    string
	APIKey   string
	BaseURL  string
}

// ServerConfig holds HTTP server configuration settings.
type ServerConfig struct {
	Host         string
	Port         int
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// LogConfig holds logging configuration settings.
type LogConfig struct {
	Level  string
	Format string
}

// AppConfig holds application-specific configuration settings.
type AppConfig struct {
	ConfirmTimeoutSecs                 int
	ConfirmAdminTimeoutSecs            int
	QueueTrackApprovalTimeoutSecs      int
	MaxQueueTrackReplacements          int
	Language                           string // Bot language for user-facing messages
	QueueAheadDurationSecs             int    // Target queue duration in seconds
	QueueCheckIntervalSecs             int    // Queue check interval in seconds
	ShadowQueueMaintenanceIntervalSecs int    // Shadow queue maintenance interval in seconds
	ShadowQueueMaxAgeHours             int    // Maximum age of shadow queue items in hours
	QueueSyncWarningTimeoutMinutes     int    // Timeout for queue sync warning in minutes
	FloodLimitPerMinute                int    // Maximum messages per user per minute (default: 6)
}

// DefaultConfig returns a new Config instance with sensible default values.
func DefaultConfig() *Config {
	return &Config{
		Telegram: TelegramConfig{
			// Telegram is always required
		},
		Spotify: SpotifyConfig{
			RedirectURL: "", // Will be dynamically generated based on server config
			TokenPath:   "./spotify_token.json",
		},
		LLM: LLMConfig{
			Provider: "", // Must be explicitly configured - no default
			Model:    "",
		},
		Server: ServerConfig{
			Host:         "127.0.0.1",
			Port:         DefaultServerPort,
			ReadTimeout:  DefaultTimeoutSeconds * time.Second,
			WriteTimeout: DefaultTimeoutSeconds * time.Second,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
		App: AppConfig{
			ConfirmTimeoutSecs:                 DefaultConfirmTimeoutSecs,
			ConfirmAdminTimeoutSecs:            DefaultConfirmAdminTimeoutSecs,
			QueueTrackApprovalTimeoutSecs:      DefaultQueueTrackApprovalTimeoutSecs,
			MaxQueueTrackReplacements:          DefaultMaxQueueTrackReplacements,
			Language:                           i18n.DefaultLanguage, // Default to English
			QueueAheadDurationSecs:             DefaultQueueAheadDurationSecs,
			QueueCheckIntervalSecs:             DefaultQueueCheckIntervalSecs,
			ShadowQueueMaintenanceIntervalSecs: DefaultShadowQueueMaintenanceIntervalSecs,
			ShadowQueueMaxAgeHours:             DefaultShadowQueueMaxAgeHours,
			QueueSyncWarningTimeoutMinutes:     DefaultQueueSyncWarningTimeoutMinutes,
			FloodLimitPerMinute:                DefaultFloodLimitPerMinute,
		},
	}
}
