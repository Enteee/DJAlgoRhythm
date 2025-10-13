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

	"whatdj/internal/core"
	httpserver "whatdj/internal/http"
	"whatdj/internal/llm"
	"whatdj/internal/spotify"
	"whatdj/internal/store"
	"whatdj/internal/whatsapp"
)

var (
	cfgFile string
	config  *core.Config
	logger  *zap.Logger
)

var rootCmd = &cobra.Command{
	Use:   "whatdj",
	Short: "WhatDj v2 - Live WhatsApp → Spotify DJ",
	Long: `WhatDj v2 is a production-grade service that listens to WhatsApp group messages
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
	rootCmd.PersistentFlags().String("whatsapp-group-jid", "", "WhatsApp group JID")
	rootCmd.PersistentFlags().String("whatsapp-device-name", "WhatDj", "WhatsApp device name")
	rootCmd.PersistentFlags().String("spotify-client-id", "", "Spotify client ID")
	rootCmd.PersistentFlags().String("spotify-client-secret", "", "Spotify client secret")
	rootCmd.PersistentFlags().String("spotify-playlist-id", "", "Spotify playlist ID")
	rootCmd.PersistentFlags().String("llm-provider", "none", "LLM provider (openai, anthropic, ollama, none)")
	rootCmd.PersistentFlags().String("llm-model", "", "LLM model name")
	rootCmd.PersistentFlags().String("llm-api-key", "", "LLM API key")
	rootCmd.PersistentFlags().Int("server-port", 8080, "HTTP server port")
	rootCmd.PersistentFlags().Int("confirm-timeout", 120, "Confirmation timeout in seconds")

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

	cfg.WhatsApp.GroupJID = viper.GetString("whatsapp-group-jid")
	cfg.WhatsApp.GroupName = viper.GetString("whatsapp-group-name")
	cfg.WhatsApp.DeviceName = viper.GetString("whatsapp-device-name")
	cfg.WhatsApp.SessionPath = viper.GetString("whatsapp-session-path")
	if cfg.WhatsApp.SessionPath == "" {
		cfg.WhatsApp.SessionPath = "./whatsapp_session.db"
	}

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

	cfg.App.ConfirmTimeoutSecs = viper.GetInt("confirm-timeout")
	cfg.App.MaxRetries = viper.GetInt("max-retries")
	if cfg.App.MaxRetries == 0 {
		cfg.App.MaxRetries = 3
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
		zap.String("spotify_playlist", config.Spotify.PlaylistID))

	if err := validateConfig(); err != nil {
		return fmt.Errorf("configuration validation failed: %w", err)
	}

	dedup := store.NewDedupStore(10000, 0.001)

	whatsappClient := whatsapp.NewClient(&config.WhatsApp, logger.Named("whatsapp"))

	spotifyClient := spotify.NewClient(&config.Spotify, logger.Named("spotify"))
	if err := spotifyClient.Authenticate(ctx); err != nil {
		return fmt.Errorf("failed to authenticate with Spotify: %w", err)
	}

	var llmProvider core.LLMProvider
	if config.LLM.Provider != noneProvider && config.LLM.Provider != "" {
		provider, err := llm.NewProvider(&config.LLM, logger.Named("llm"))
		if err != nil {
			return fmt.Errorf("failed to create LLM provider: %w", err)
		}
		llmProvider = provider
	}

	httpServer := httpserver.NewServer(&config.Server, logger.Named("http"))

	orchestrator := core.NewOrchestrator(
		config,
		whatsappClient,
		spotifyClient,
		llmProvider,
		dedup,
		logger.Named("orchestrator"),
	)

	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return httpServer.Start(gCtx)
	})

	g.Go(func() error {
		return orchestrator.Start(gCtx)
	})

	g.Go(func() error {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-gCtx.Done():
				return nil
			case <-ticker.C:
				httpServer.SetPlaylistSize(dedup.Size())
			}
		}
	})

	logger.Info("WhatDj v2 started successfully",
		zap.String("http_addr", fmt.Sprintf("%s:%d", config.Server.Host, config.Server.Port)))

	if err := g.Wait(); err != nil {
		logger.Error("WhatDj v2 stopped with error", zap.Error(err))
		return err
	}

	logger.Info("WhatDj v2 stopped gracefully")
	return nil
}

func validateConfig() error {
	if config.WhatsApp.GroupJID == "" {
		return fmt.Errorf("WhatsApp group JID is required")
	}

	if config.Spotify.ClientID == "" {
		return fmt.Errorf("spotify client ID is required")
	}

	if config.Spotify.ClientSecret == "" {
		return fmt.Errorf("spotify client secret is required")
	}

	if config.Spotify.PlaylistID == "" {
		return fmt.Errorf("spotify playlist ID is required")
	}

	if config.LLM.Provider != noneProvider && config.LLM.Provider != "" {
		if config.LLM.APIKey == "" && config.LLM.Provider != "ollama" {
			return fmt.Errorf("LLM API key is required for provider: %s", config.LLM.Provider)
		}
	}

	return nil
}
