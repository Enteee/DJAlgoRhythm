// Package main provides the WhatDj v2 CLI application entry point.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"

	"whatdj/internal/chat"
	"whatdj/internal/chat/telegram"
	"whatdj/internal/chat/whatsapp"
	"whatdj/internal/core"
	httpserver "whatdj/internal/http"
	"whatdj/internal/i18n"
	"whatdj/internal/llm"
	"whatdj/internal/spotify"
	"whatdj/internal/store"
)

var (
	cfgFile string
	config  *core.Config
	logger  *zap.Logger
)

var rootCmd = &cobra.Command{
	Use:   "whatdj",
	Short: "WhatDj v2 - Live Chat → Spotify DJ",
	Long: `WhatDj v2 is a production-grade service that listens to chat messages (Telegram/WhatsApp)
and automatically adds requested tracks to a Spotify playlist with AI disambiguation.`,
	RunE: runWhatDj,
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is .env)")
	rootCmd.PersistentFlags().String("log-level", "info", "log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().Bool("whatsapp-enabled", false, "Enable WhatsApp integration")
	rootCmd.PersistentFlags().String("whatsapp-group-jid", "", "WhatsApp group JID")
	rootCmd.PersistentFlags().String("whatsapp-device-name", "WhatDj", "WhatsApp device name")
	rootCmd.PersistentFlags().Bool("telegram-enabled", true, "Enable Telegram integration")
	rootCmd.PersistentFlags().String("telegram-bot-token", "", "Telegram bot token")
	rootCmd.PersistentFlags().Int64("telegram-group-id", 0, "Telegram group ID")
	rootCmd.PersistentFlags().String("spotify-client-id", "", "Spotify client ID")
	rootCmd.PersistentFlags().String("spotify-client-secret", "", "Spotify client secret")
	rootCmd.PersistentFlags().String("spotify-playlist-id", "", "Spotify playlist ID")
	rootCmd.PersistentFlags().String("llm-provider", "none", "LLM provider (openai, anthropic, ollama, none)")
	rootCmd.PersistentFlags().String("llm-model", "", "LLM model name")
	rootCmd.PersistentFlags().String("llm-api-key", "", "LLM API key")
	rootCmd.PersistentFlags().Int("server-port", 8080, "HTTP server port")
	rootCmd.PersistentFlags().Int("confirm-timeout-secs", 120, "Confirmation timeout in seconds")
	rootCmd.PersistentFlags().Int("confirm-admin-timeout-secs", 3600, "Admin confirmation timeout in seconds")
	rootCmd.PersistentFlags().String("language", i18n.DefaultLanguage, fmt.Sprintf("Bot language (%s)", strings.Join(i18n.GetSupportedLanguages(), ", ")))

	if err := viper.BindPFlags(rootCmd.PersistentFlags()); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to bind flags: %v\n", err)
		os.Exit(1)
	}
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.AddConfigPath(".")
		viper.SetConfigName(".env")
		viper.SetConfigType("env")
	}

	viper.SetEnvPrefix("WHATDJ")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			fmt.Fprintf(os.Stderr, "Error reading config file: %v\n", err)
			os.Exit(1)
		}
	}

	config = buildConfig()
	logger = buildLogger(config.Log.Level)
}

func buildConfig() *core.Config {
	cfg := core.DefaultConfig()

	cfg.WhatsApp.Enabled = viper.GetBool("whatsapp-enabled")
	cfg.WhatsApp.GroupJID = viper.GetString("whatsapp-group-jid")
	cfg.WhatsApp.DeviceName = viper.GetString("whatsapp-device-name")
	cfg.WhatsApp.SessionPath = viper.GetString("whatsapp-session-path")
	if cfg.WhatsApp.SessionPath == "" {
		cfg.WhatsApp.SessionPath = "./whatsapp_session.db"
	}

	cfg.Telegram.Enabled = viper.GetBool("telegram-enabled")
	cfg.Telegram.BotToken = viper.GetString("telegram-bot-token")
	cfg.Telegram.GroupID = viper.GetInt64("telegram-group-id")
	cfg.Telegram.ReactionSupport = viper.GetBool("telegram-reaction-support")
	if !viper.IsSet("telegram-reaction-support") {
		cfg.Telegram.ReactionSupport = true // default to true
	}
	cfg.Telegram.AdminApproval = viper.GetBool("admin-approval")

	cfg.Spotify.ClientID = viper.GetString("spotify-client-id")
	cfg.Spotify.ClientSecret = viper.GetString("spotify-client-secret")
	cfg.Spotify.RedirectURL = viper.GetString("spotify-redirect-url")
	cfg.Spotify.PlaylistID = viper.GetString("spotify-playlist-id")
	cfg.Spotify.TokenPath = viper.GetString("spotify-token-path")
	if cfg.Spotify.TokenPath == "" {
		cfg.Spotify.TokenPath = "./spotify_token.json"
	}

	cfg.LLM.Provider = viper.GetString("llm-provider")
	cfg.LLM.Model = viper.GetString("llm-model")
	cfg.LLM.APIKey = viper.GetString("llm-api-key")
	cfg.LLM.BaseURL = viper.GetString("llm-base-url")
	cfg.LLM.Threshold = viper.GetFloat64("llm-threshold")
	if cfg.LLM.Threshold == 0 {
		cfg.LLM.Threshold = 0.65
	}

	cfg.Server.Host = viper.GetString("server-host")
	if cfg.Server.Host == "" {
		cfg.Server.Host = "0.0.0.0"
	}
	cfg.Server.Port = viper.GetInt("server-port")

	cfg.Log.Level = viper.GetString("log-level")

	cfg.App.ConfirmTimeoutSecs = viper.GetInt("confirm-timeout-secs")
	cfg.App.ConfirmAdminTimeoutSecs = viper.GetInt("confirm-admin-timeout-secs")
	cfg.App.MaxRetries = viper.GetInt("max-retries")
	if cfg.App.MaxRetries == 0 {
		cfg.App.MaxRetries = 3
	}

	// Language configuration with validation
	cfg.App.Language = viper.GetString("language")
	if cfg.App.Language == "" {
		cfg.App.Language = i18n.DefaultLanguage
	}

	// Validate that the specified language is supported
	supportedLanguages := i18n.GetSupportedLanguages()
	isSupported := false
	for _, lang := range supportedLanguages {
		if cfg.App.Language == lang {
			isSupported = true
			break
		}
	}
	if !isSupported {
		fmt.Fprintf(os.Stderr, "Warning: Unsupported language '%s', falling back to '%s'. Supported languages: %s\n",
			cfg.App.Language, i18n.DefaultLanguage, strings.Join(supportedLanguages, ", "))
		cfg.App.Language = i18n.DefaultLanguage
	}

	return cfg
}

func buildLogger(level string) *zap.Logger {
	var zapLevel zapcore.Level
	switch strings.ToLower(level) {
	case "debug":
		zapLevel = zapcore.DebugLevel
	case "info":
		zapLevel = zapcore.InfoLevel
	case "warn":
		zapLevel = zapcore.WarnLevel
	case "error":
		zapLevel = zapcore.ErrorLevel
	default:
		zapLevel = zapcore.InfoLevel
	}

	cfg := zap.NewProductionConfig()
	cfg.Level = zap.NewAtomicLevelAt(zapLevel)

	builtLogger, err := cfg.Build()
	if err != nil {
		panic(fmt.Sprintf("Failed to build logger: %v", err))
	}

	return builtLogger
}

const noneProvider = "none"

func runWhatDj(_ *cobra.Command, _ []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger.Info("Starting WhatDj v2",
		zap.String("version", "2.0.0"),
		zap.String("llm_provider", config.LLM.Provider),
		zap.String("spotify_playlist", config.Spotify.PlaylistID),
		zap.Bool("telegram_enabled", config.Telegram.Enabled),
		zap.Bool("whatsapp_enabled", config.WhatsApp.Enabled))

	if err := validateConfig(); err != nil {
		return fmt.Errorf("configuration validation failed: %w", err)
	}

	services, err := initializeServices(ctx)
	if err != nil {
		return err
	}

	return runServices(ctx, services)
}

type services struct {
	frontend   chat.Frontend
	spotify    *spotify.Client
	llm        core.LLMProvider
	httpServer *httpserver.Server
	dispatcher *core.Dispatcher
	dedup      *store.DedupStore
}

func initializeServices(ctx context.Context) (*services, error) {
	dedup := store.NewDedupStore(10000, 0.001)

	frontend, err := createChatFrontend()
	if err != nil {
		return nil, err
	}

	spotifyClient := spotify.NewClient(&config.Spotify, logger.Named("spotify"))
	if authErr := spotifyClient.Authenticate(ctx); authErr != nil {
		return nil, fmt.Errorf("failed to authenticate with Spotify: %w", authErr)
	}

	llmProvider, err := createLLMProvider()
	if err != nil {
		return nil, err
	}

	httpServer := httpserver.NewServer(&config.Server, logger.Named("http"))
	dispatcher := core.NewDispatcher(config, frontend, spotifyClient, llmProvider, dedup,
		logger.Named("dispatcher"))

	return &services{
		frontend:   frontend,
		spotify:    spotifyClient,
		llm:        llmProvider,
		httpServer: httpServer,
		dispatcher: dispatcher,
		dedup:      dedup,
	}, nil
}

func createChatFrontend() (chat.Frontend, error) {
	if config.Telegram.Enabled {
		telegramConfig := &telegram.Config{
			BotToken:        config.Telegram.BotToken,
			GroupID:         config.Telegram.GroupID,
			Enabled:         config.Telegram.Enabled,
			ReactionSupport: config.Telegram.ReactionSupport,
			AdminApproval:   config.Telegram.AdminApproval,
			Language:        config.App.Language,
		}
		frontend := telegram.NewFrontend(telegramConfig, logger.Named("telegram"))
		logger.Info("Using Telegram as primary chat frontend",
			zap.Bool("admin_approval", config.Telegram.AdminApproval),
			zap.String("language", config.App.Language))
		return frontend, nil
	}

	if config.WhatsApp.Enabled {
		whatsappConfig := &whatsapp.Config{
			GroupJID:    config.WhatsApp.GroupJID,
			DeviceName:  config.WhatsApp.DeviceName,
			SessionPath: config.WhatsApp.SessionPath,
			Enabled:     config.WhatsApp.Enabled,
			Language:    config.App.Language,
		}
		frontend := whatsapp.NewFrontend(whatsappConfig, logger.Named("whatsapp"))
		logger.Info("Using WhatsApp as chat frontend",
			zap.String("language", config.App.Language))
		return frontend, nil
	}

	return nil, fmt.Errorf("no chat frontend enabled - enable either Telegram or WhatsApp")
}

func createLLMProvider() (core.LLMProvider, error) {
	if config.LLM.Provider != noneProvider && config.LLM.Provider != "" {
		provider, err := llm.NewProvider(&config.LLM, logger.Named("llm"))
		if err != nil {
			return nil, fmt.Errorf("failed to create LLM provider: %w", err)
		}
		return provider, nil
	}
	return nil, nil
}

func runServices(ctx context.Context, svcs *services) error {
	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return svcs.httpServer.Start(gCtx)
	})

	g.Go(func() error {
		return svcs.dispatcher.Start(gCtx)
	})

	g.Go(func() error {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-gCtx.Done():
				return nil
			case <-ticker.C:
				svcs.httpServer.SetPlaylistSize(svcs.dedup.Size())
			}
		}
	})

	logger.Info("WhatDj v2 started successfully",
		zap.String("http_addr", fmt.Sprintf("%s:%d", config.Server.Host, config.Server.Port)))

	if err := g.Wait(); err != nil {
		logger.Error("WhatDj v2 stopped with error", zap.Error(err))
		// Still call Stop to send shutdown message
		if stopErr := svcs.dispatcher.Stop(context.Background()); stopErr != nil {
			logger.Debug("Failed to stop dispatcher gracefully", zap.Error(stopErr))
		}
		return err
	}

	// Graceful shutdown - call Stop to send shutdown message
	if err := svcs.dispatcher.Stop(context.Background()); err != nil {
		logger.Debug("Failed to stop dispatcher gracefully", zap.Error(err))
	}

	logger.Info("WhatDj v2 stopped gracefully")
	return nil
}

func validateConfig() error {
	// Ensure at least one chat frontend is enabled
	if !config.Telegram.Enabled && !config.WhatsApp.Enabled {
		return fmt.Errorf("at least one chat frontend must be enabled (Telegram or WhatsApp)")
	}

	// Validate Telegram configuration if enabled
	if config.Telegram.Enabled {
		if config.Telegram.BotToken == "" {
			return fmt.Errorf("telegram bot token is required when Telegram is enabled")
		}
		if config.Telegram.GroupID == 0 {
			return fmt.Errorf("telegram group ID is required when Telegram is enabled")
		}
	}

	// Validate WhatsApp configuration if enabled
	if config.WhatsApp.Enabled {
		if config.WhatsApp.GroupJID == "" {
			return fmt.Errorf("WhatsApp group JID is required when WhatsApp is enabled")
		}
	}

	// Validate Spotify configuration (always required)
	if config.Spotify.ClientID == "" {
		return fmt.Errorf("spotify client ID is required")
	}

	if config.Spotify.ClientSecret == "" {
		return fmt.Errorf("spotify client secret is required")
	}

	if config.Spotify.PlaylistID == "" {
		return fmt.Errorf("spotify playlist ID is required")
	}

	// Validate LLM configuration if enabled
	if config.LLM.Provider != noneProvider && config.LLM.Provider != "" {
		if config.LLM.APIKey == "" && config.LLM.Provider != "ollama" {
			return fmt.Errorf("LLM API key is required for provider: %s", config.LLM.Provider)
		}
	}

	return nil
}
