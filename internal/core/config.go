// Package core provides the main business logic and configuration for WhatDj.
package core

import (
	"time"

	"whatdj/internal/i18n"
)

// Default configuration values
const (
	DefaultLLMThreshold            = 0.65
	DefaultMaxCandidates           = 3
	DefaultServerPort              = 8080
	DefaultTimeoutSeconds          = 10
	DefaultConfirmTimeoutSecs      = 120
	DefaultConfirmAdminTimeoutSecs = 3600
	DefaultMaxRetries              = 3
	DefaultRetryDelaySecs          = 5
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
	BotToken        string
	GroupID         int64
	Enabled         bool
	ReactionSupport bool
	AdminApproval   bool
}

type SpotifyConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	PlaylistID   string
	TokenPath    string
}

type LLMConfig struct {
	Provider      string
	Model         string
	APIKey        string
	BaseURL       string
	Threshold     float64
	MaxCandidates int
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
	ConfirmTimeoutSecs      int
	ConfirmAdminTimeoutSecs int
	MaxRetries              int
	RetryDelaySecs          int
	Language                string // Bot language for user-facing messages
}

func DefaultConfig() *Config {
	return &Config{
		WhatsApp: WhatsAppConfig{
			DeviceName:  "WhatDj",
			SessionPath: "./whatsapp_session.db",
			Enabled:     false, // Disabled by default
		},
		Telegram: TelegramConfig{
			Enabled:         true, // Enabled by default
			ReactionSupport: true,
		},
		Spotify: SpotifyConfig{
			RedirectURL: "", // Will be dynamically generated based on server config
			TokenPath:   "./spotify_token.json",
		},
		LLM: LLMConfig{
			Provider:      "none",
			Model:         "",
			Threshold:     DefaultLLMThreshold,
			MaxCandidates: DefaultMaxCandidates,
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
			ConfirmTimeoutSecs:      DefaultConfirmTimeoutSecs,
			ConfirmAdminTimeoutSecs: DefaultConfirmAdminTimeoutSecs,
			MaxRetries:              DefaultMaxRetries,
			RetryDelaySecs:          DefaultRetryDelaySecs,
			Language:                i18n.DefaultLanguage, // Default to English
		},
	}
}
