// Package core provides the main business logic and configuration for DJAlgoRhythm.
package core

import (
	"time"

	"djalgorhythm/internal/i18n"
)

// Default configuration values
const (
	DefaultServerPort                         = 8080
	DefaultTimeoutSeconds                     = 10
	DefaultConfirmTimeoutSecs                 = 120
	DefaultConfirmAdminTimeoutSecs            = 3600
	DefaultQueueTrackApprovalTimeoutSecs      = 30
	DefaultMaxQueueTrackReplacements          = 3
	DefaultRetryDelaySecs                     = 5
	DefaultQueueAheadDurationSecs             = 90
	DefaultQueueCheckIntervalSecs             = 45
	DefaultShadowQueueMaintenanceIntervalSecs = 30
	DefaultShadowQueueMaxAgeHours             = 2
	DefaultShadowQueuePreferenceEnabled       = true
	DefaultQueueSyncWarningTimeoutMinutes     = 30
	DefaultFloodLimitPerMinute                = 6
)

type Config struct {
	WhatsApp WhatsAppConfig
	Telegram TelegramConfig
	Spotify  SpotifyConfig
	LLM      LLMConfig
	Server   ServerConfig
	Log      LogConfig
	App      AppConfig
}

type WhatsAppConfig struct {
	GroupJID    string
	DeviceName  string
	SessionPath string
	Enabled     bool
}

type TelegramConfig struct {
	BotToken           string
	GroupID            int64
	Enabled            bool
	AdminApproval      bool
	AdminNeedsApproval bool
	CommunityApproval  int
}

type SpotifyConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	PlaylistID   string
	TokenPath    string
}

type LLMConfig struct {
	Provider string
	Model    string
	APIKey   string
	BaseURL  string
}

type ServerConfig struct {
	Host         string
	Port         int
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

type LogConfig struct {
	Level  string
	Format string
}

type AppConfig struct {
	ConfirmTimeoutSecs                 int
	ConfirmAdminTimeoutSecs            int
	QueueTrackApprovalTimeoutSecs      int
	MaxQueueTrackReplacements          int
	RetryDelaySecs                     int
	Language                           string // Bot language for user-facing messages
	QueueAheadDurationSecs             int    // Target queue duration in seconds
	QueueCheckIntervalSecs             int    // Queue check interval in seconds
	ShadowQueueMaintenanceIntervalSecs int    // Shadow queue maintenance interval in seconds
	ShadowQueueMaxAgeHours             int    // Maximum age of shadow queue items in hours
	ShadowQueuePreferenceEnabled       bool   // Prefer shadow queue over Spotify API for position/duration
	QueueSyncWarningTimeoutMinutes     int    // Timeout for queue sync warning in minutes
	FloodLimitPerMinute                int    // Maximum messages per user per minute (default: 6)
}

func DefaultConfig() *Config {
	return &Config{
		WhatsApp: WhatsAppConfig{
			DeviceName:  "DJAlgoRhythm",
			SessionPath: "./whatsapp_session.db",
			Enabled:     false, // Disabled by default
		},
		Telegram: TelegramConfig{
			Enabled: true, // Enabled by default
		},
		Spotify: SpotifyConfig{
			RedirectURL: "", // Will be dynamically generated based on server config
			TokenPath:   "./spotify_token.json",
		},
		LLM: LLMConfig{
			Provider: "none",
			Model:    "",
		},
		Server: ServerConfig{
			Host:         "0.0.0.0",
			Port:         DefaultServerPort,
			ReadTimeout:  DefaultTimeoutSeconds * time.Second,
			WriteTimeout: DefaultTimeoutSeconds * time.Second,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "json",
		},
		App: AppConfig{
			ConfirmTimeoutSecs:                 DefaultConfirmTimeoutSecs,
			ConfirmAdminTimeoutSecs:            DefaultConfirmAdminTimeoutSecs,
			QueueTrackApprovalTimeoutSecs:      DefaultQueueTrackApprovalTimeoutSecs,
			MaxQueueTrackReplacements:          DefaultMaxQueueTrackReplacements,
			RetryDelaySecs:                     DefaultRetryDelaySecs,
			Language:                           i18n.DefaultLanguage, // Default to English
			QueueAheadDurationSecs:             DefaultQueueAheadDurationSecs,
			QueueCheckIntervalSecs:             DefaultQueueCheckIntervalSecs,
			ShadowQueueMaintenanceIntervalSecs: DefaultShadowQueueMaintenanceIntervalSecs,
			ShadowQueueMaxAgeHours:             DefaultShadowQueueMaxAgeHours,
			ShadowQueuePreferenceEnabled:       DefaultShadowQueuePreferenceEnabled,
			QueueSyncWarningTimeoutMinutes:     DefaultQueueSyncWarningTimeoutMinutes,
			FloodLimitPerMinute:                DefaultFloodLimitPerMinute,
		},
	}
}
