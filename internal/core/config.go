// Package core provides the main business logic and configuration for WhatDj.
package core

import (
	"time"
)

// Default configuration values
const (
	DefaultLLMThreshold       = 0.65
	DefaultMaxCandidates      = 3
	DefaultServerPort         = 8080
	DefaultTimeoutSeconds     = 10
	DefaultConfirmTimeoutSecs = 120
	DefaultMaxRetries         = 3
	DefaultRetryDelaySecs     = 5
)

type Config struct {
	WhatsApp WhatsAppConfig
	Spotify  SpotifyConfig
	LLM      LLMConfig
	Server   ServerConfig
	Log      LogConfig
	App      AppConfig
}

type WhatsAppConfig struct {
	GroupJID    string
	GroupName   string
	DeviceName  string
	SessionPath string
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
	ConfirmTimeoutSecs int
	MaxRetries         int
	RetryDelaySecs     int
}

func DefaultConfig() *Config {
	return &Config{
		WhatsApp: WhatsAppConfig{
			DeviceName:  "WhatDj",
			SessionPath: "./whatsapp_session.db",
		},
		Spotify: SpotifyConfig{
			RedirectURL: "http://localhost:8080/callback",
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
			ConfirmTimeoutSecs: DefaultConfirmTimeoutSecs,
			MaxRetries:         DefaultMaxRetries,
			RetryDelaySecs:     DefaultRetryDelaySecs,
		},
	}
}
