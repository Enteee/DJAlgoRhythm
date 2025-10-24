// Package main provides the DJAlgoRhythm CLI application entry point.
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
	"github.com/subosito/gotenv"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"

	"djalgorhythm/internal/chat"
	"djalgorhythm/internal/chat/telegram"
	"djalgorhythm/internal/chat/whatsapp"
	"djalgorhythm/internal/core"
	httpserver "djalgorhythm/internal/http"
	"djalgorhythm/internal/i18n"
	"djalgorhythm/internal/llm"
	"djalgorhythm/internal/spotify"
	"djalgorhythm/internal/store"
)

const (
	defaultServerHost = "0.0.0.0"
)

var (
	cfgFile string
	config  *core.Config
	logger  *zap.Logger
)

var rootCmd = &cobra.Command{
	Use:   "djalgorhythm",
	Short: "DJAlgoRhythm - Live Chat → Spotify DJ",
	Long: `DJAlgoRhythm is a production-grade service that listens to chat messages (Telegram/WhatsApp)
and automatically adds requested tracks to a Spotify playlist with AI disambiguation.`,
	RunE: runDJAlgoRhythm,
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
	rootCmd.PersistentFlags().String("whatsapp-device-name", "DJAlgoRhythm", "WhatsApp device name")
	rootCmd.PersistentFlags().Bool("telegram-enabled", true, "Enable Telegram integration")
	rootCmd.PersistentFlags().String("telegram-bot-token", "", "Telegram bot token")
	rootCmd.PersistentFlags().Int64("telegram-group-id", 0, "Telegram group ID")
	rootCmd.PersistentFlags().String("spotify-client-id", "", "Spotify client ID")
	rootCmd.PersistentFlags().String("spotify-client-secret", "", "Spotify client secret")
	rootCmd.PersistentFlags().String("spotify-playlist-id", "", "Spotify playlist ID")
	rootCmd.PersistentFlags().String("llm-provider", "none", "LLM provider (openai, anthropic, ollama, none)")
	rootCmd.PersistentFlags().String("llm-model", "", "LLM model name")
	rootCmd.PersistentFlags().String("llm-api-key", "", "LLM API key")
	rootCmd.PersistentFlags().String("server-host", defaultServerHost, "HTTP server host")
	rootCmd.PersistentFlags().Int("server-port", 8080, "HTTP server port")
	rootCmd.PersistentFlags().Int("confirm-timeout-secs", 120, "Confirmation timeout in seconds")
	rootCmd.PersistentFlags().Int("confirm-admin-timeout-secs", 3600, "Admin confirmation timeout in seconds")
	rootCmd.PersistentFlags().Int("queue-track-approval-timeout-secs", 30, "Queue track approval timeout in seconds")
	rootCmd.PersistentFlags().Int("max-queue-track-replacements", 3, "Maximum queue track replacement attempts before auto-accepting")
	rootCmd.PersistentFlags().Bool("admin-needs-approval", false, "Require approval even for admins (for testing)")
	rootCmd.PersistentFlags().Int("community-approval", 0, "Number of 👍 reactions needed to bypass admin approval (0 disables feature)")
	rootCmd.PersistentFlags().Int("queue-ahead-duration-secs", 90, "Target queue duration in seconds")
	rootCmd.PersistentFlags().Int("queue-check-interval-secs", 45, "Queue check interval in seconds")
	rootCmd.PersistentFlags().Int("shadow-queue-maintenance-interval-mins", 5, "Shadow queue maintenance interval in minutes")
	rootCmd.PersistentFlags().Int("shadow-queue-max-age-hours", 2, "Maximum age of shadow queue items in hours")
	supportedLangs := strings.Join(i18n.GetSupportedLanguages(), ", ")
	rootCmd.PersistentFlags().String("language", i18n.DefaultLanguage, fmt.Sprintf("Bot language (%s)", supportedLangs))
	rootCmd.PersistentFlags().Int("flood-limit-per-minute", 6, "Maximum messages per user per minute")
	rootCmd.PersistentFlags().Bool("generate-env-example", false, "Generate .env.example file from current configuration and exit")

	if err := viper.BindPFlags(rootCmd.PersistentFlags()); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to bind flags: %v\n", err)
		os.Exit(1)
	}
}

func initConfig() {
	// Load .env file explicitly using gotenv
	envFile := ".env"
	if cfgFile != "" {
		envFile = cfgFile
	}

	if err := gotenv.Load(envFile); err != nil {
		// Don't exit if .env file doesn't exist, just warn
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Error loading .env file: %v\n", err)
		}
	}

	viper.SetEnvPrefix("DJALGORHYTHM")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()

	config = buildConfig()
	logger = buildLogger(config.Log.Level)
}

func buildConfig() *core.Config {
	cfg := core.DefaultConfig()

	configureWhatsApp(cfg)
	configureTelegram(cfg)
	configureSpotify(cfg)
	configureLLM(cfg)
	configureServer(cfg)
	configureApp(cfg)

	return cfg
}

func configureWhatsApp(cfg *core.Config) {
	cfg.WhatsApp.Enabled = viper.GetBool("whatsapp-enabled")
	cfg.WhatsApp.GroupJID = viper.GetString("whatsapp-group-jid")
	cfg.WhatsApp.DeviceName = viper.GetString("whatsapp-device-name")
	cfg.WhatsApp.SessionPath = viper.GetString("whatsapp-session-path")
	if cfg.WhatsApp.SessionPath == "" {
		cfg.WhatsApp.SessionPath = "./whatsapp_session.db"
	}
}

func configureTelegram(cfg *core.Config) {
	cfg.Telegram.Enabled = viper.GetBool("telegram-enabled")
	cfg.Telegram.BotToken = viper.GetString("telegram-bot-token")
	cfg.Telegram.GroupID = viper.GetInt64("telegram-group-id")
	cfg.Telegram.AdminApproval = viper.GetBool("admin-approval")
	cfg.Telegram.AdminNeedsApproval = viper.GetBool("admin-needs-approval")
	cfg.Telegram.CommunityApproval = viper.GetInt("community-approval")
}

func configureSpotify(cfg *core.Config) {
	cfg.Spotify.ClientID = viper.GetString("spotify-client-id")
	cfg.Spotify.ClientSecret = viper.GetString("spotify-client-secret")
	cfg.Spotify.RedirectURL = viper.GetString("spotify-redirect-url")
	cfg.Spotify.PlaylistID = viper.GetString("spotify-playlist-id")
	cfg.Spotify.TokenPath = viper.GetString("spotify-token-path")
	if cfg.Spotify.TokenPath == "" {
		cfg.Spotify.TokenPath = "./spotify_token.json"
	}

	// Build default redirect URL based on server configuration if not explicitly set
	if cfg.Spotify.RedirectURL == "" {
		serverHost := cfg.Server.Host
		if serverHost == defaultServerHost {
			serverHost = "127.0.0.1" // Use localhost for OAuth callback
		}
		cfg.Spotify.RedirectURL = fmt.Sprintf("http://%s:%d/callback", serverHost, cfg.Server.Port)
	}
}

func configureLLM(cfg *core.Config) {
	cfg.LLM.Provider = viper.GetString("llm-provider")
	cfg.LLM.Model = viper.GetString("llm-model")
	cfg.LLM.APIKey = viper.GetString("llm-api-key")
	cfg.LLM.BaseURL = viper.GetString("llm-base-url")
}

func configureServer(cfg *core.Config) {
	cfg.Server.Host = viper.GetString("server-host")
	if cfg.Server.Host == "" {
		cfg.Server.Host = defaultServerHost
	}
	cfg.Server.Port = viper.GetInt("server-port")
	cfg.Log.Level = viper.GetString("log-level")
}

func configureApp(cfg *core.Config) {
	cfg.App.ConfirmTimeoutSecs = viper.GetInt("confirm-timeout-secs")
	cfg.App.ConfirmAdminTimeoutSecs = viper.GetInt("confirm-admin-timeout-secs")
	cfg.App.QueueTrackApprovalTimeoutSecs = viper.GetInt("queue-track-approval-timeout-secs")
	cfg.App.MaxQueueTrackReplacements = viper.GetInt("max-queue-track-replacements")

	// Queue-ahead configuration
	cfg.App.QueueAheadDurationSecs = viper.GetInt("queue-ahead-duration-secs")
	cfg.App.QueueCheckIntervalSecs = viper.GetInt("queue-check-interval-secs")

	// Shadow queue configuration
	cfg.App.ShadowQueueMaintenanceIntervalSecs = viper.GetInt("shadow-queue-maintenance-interval-secs")
	if cfg.App.ShadowQueueMaintenanceIntervalSecs <= 0 {
		fmt.Printf("Warning: Invalid shadow queue maintenance interval (%d), using default (%d)\n",
			cfg.App.ShadowQueueMaintenanceIntervalSecs, core.DefaultShadowQueueMaintenanceIntervalSecs)
		cfg.App.ShadowQueueMaintenanceIntervalSecs = core.DefaultShadowQueueMaintenanceIntervalSecs
	}
	cfg.App.ShadowQueueMaxAgeHours = viper.GetInt("shadow-queue-max-age-hours")
	if cfg.App.ShadowQueueMaxAgeHours <= 0 {
		fmt.Printf("Warning: Invalid shadow queue max age (%d), using default (%d)\n",
			cfg.App.ShadowQueueMaxAgeHours, core.DefaultShadowQueueMaxAgeHours)
		cfg.App.ShadowQueueMaxAgeHours = core.DefaultShadowQueueMaxAgeHours
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

	// Flood prevention configuration
	cfg.App.FloodLimitPerMinute = viper.GetInt("flood-limit-per-minute")
	if cfg.App.FloodLimitPerMinute <= 0 {
		cfg.App.FloodLimitPerMinute = core.DefaultFloodLimitPerMinute
	}
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

func runDJAlgoRhythm(cmd *cobra.Command, _ []string) error {
	// Handle generate-env-example flag
	if viper.GetBool("generate-env-example") {
		return generateEnvExample(cmd)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger.Info("Starting DJAlgoRhythm",
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

	llmProvider, err := createLLMProvider()
	if err != nil {
		return nil, err
	}

	spotifyClient := spotify.NewClient(&config.Spotify, logger.Named("spotify"), llmProvider)
	if authErr := spotifyClient.Authenticate(ctx); authErr != nil {
		return nil, fmt.Errorf("failed to authenticate with Spotify: %w", authErr)
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
			BotToken:            config.Telegram.BotToken,
			GroupID:             config.Telegram.GroupID,
			Enabled:             config.Telegram.Enabled,
			AdminApproval:       config.Telegram.AdminApproval,
			AdminNeedsApproval:  config.Telegram.AdminNeedsApproval,
			CommunityApproval:   config.Telegram.CommunityApproval,
			Language:            config.App.Language,
			FloodLimitPerMinute: config.App.FloodLimitPerMinute,
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

	logger.Info("DJAlgoRhythm started successfully",
		zap.String("http_addr", fmt.Sprintf("%s:%d", config.Server.Host, config.Server.Port)))

	if err := g.Wait(); err != nil {
		logger.Error("DJAlgoRhythm stopped with error", zap.Error(err))
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

	logger.Info("DJAlgoRhythm stopped gracefully")
	return nil
}

func promptForTelegramGroup() (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fmt.Println("\n🤖 DJALGORHYTHM_TELEGRAM_GROUP_ID not set. Scanning for available groups...")

	// Create a temporary Telegram frontend to list groups
	telegramConfig := &telegram.Config{
		BotToken:            config.Telegram.BotToken,
		GroupID:             0, // Temporary - we'll set this after selection
		Enabled:             true,
		AdminApproval:       config.Telegram.AdminApproval,
		AdminNeedsApproval:  config.Telegram.AdminNeedsApproval,
		CommunityApproval:   config.Telegram.CommunityApproval,
		Language:            config.App.Language,
		FloodLimitPerMinute: config.App.FloodLimitPerMinute,
	}

	tempFrontend := telegram.NewFrontend(telegramConfig, logger.Named("telegram-setup"))
	if err := tempFrontend.Start(ctx); err != nil {
		return 0, fmt.Errorf("failed to initialize Telegram bot: %w", err)
	}

	// Wait a moment for the bot to be ready
	time.Sleep(2 * time.Second)

	// List available groups
	groups, err := tempFrontend.ListAvailableGroups(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to list groups: %w", err)
	}

	if len(groups) == 0 {
		return 0, fmt.Errorf("no groups found. Please add the bot to a group first and send some messages")
	}

	// Display available groups
	fmt.Println("\n📋 Available groups:")
	for i, group := range groups {
		fmt.Printf("  %d. %s (ID: %d, Type: %s)\n", i+1, group.Title, group.ID, group.Type)
	}

	// Prompt user for selection
	fmt.Printf("\nSelect a group (1-%d): ", len(groups))
	var selection int
	if _, err := fmt.Scanln(&selection); err != nil {
		return 0, fmt.Errorf("failed to read selection: %w", err)
	}

	if selection < 1 || selection > len(groups) {
		return 0, fmt.Errorf("invalid selection: %d (must be between 1 and %d)", selection, len(groups))
	}

	selectedGroup := groups[selection-1]
	fmt.Printf("\n✅ Selected group: %s (ID: %d)\n", selectedGroup.Title, selectedGroup.ID)
	fmt.Printf("💡 To avoid this prompt in the future, set: DJALGORHYTHM_TELEGRAM_GROUP_ID=%d\n\n", selectedGroup.ID)

	return selectedGroup.ID, nil
}

func validateConfig() error {
	if err := validateChatFrontends(); err != nil {
		return err
	}

	if err := validateSpotifyConfig(); err != nil {
		return err
	}

	if err := validateLLMConfig(); err != nil {
		return err
	}

	return nil
}

func validateChatFrontends() error {
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
			// Interactive group selection if group ID not provided
			groupID, err := promptForTelegramGroup()
			if err != nil {
				return fmt.Errorf("failed to select Telegram group: %w", err)
			}
			config.Telegram.GroupID = groupID
			logger.Info("Selected Telegram group interactively", zap.Int64("groupID", groupID))
		}
	}

	// Validate WhatsApp configuration if enabled
	if config.WhatsApp.Enabled {
		if config.WhatsApp.GroupJID == "" {
			return fmt.Errorf("WhatsApp group JID is required when WhatsApp is enabled")
		}
	}

	return nil
}

func validateSpotifyConfig() error {
	if config.Spotify.ClientID == "" {
		return fmt.Errorf("spotify client ID is required")
	}

	if config.Spotify.ClientSecret == "" {
		return fmt.Errorf("spotify client secret is required")
	}

	if config.Spotify.PlaylistID == "" {
		return fmt.Errorf("spotify playlist ID is required")
	}

	return nil
}

func validateLLMConfig() error {
	if config.LLM.Provider != noneProvider && config.LLM.Provider != "" {
		if config.LLM.APIKey == "" && config.LLM.Provider != "ollama" {
			return fmt.Errorf("LLM API key is required for provider: %s", config.LLM.Provider)
		}
	}
	return nil
}

func generateEnvExample(cmd *cobra.Command) error {
	fmt.Println("Generating .env.example file from current configuration...")

	content := generateEnvExampleContent(cmd)

	if err := os.WriteFile(".env.example", []byte(content), 0600); err != nil {
		return fmt.Errorf("failed to write .env.example: %w", err)
	}

	fmt.Println("✅ Successfully generated .env.example file")
	return nil
}

func generateEnvExampleContent(cmd *cobra.Command) string {
	var content strings.Builder

	// Header
	content.WriteString("# =============================================================================\n")
	content.WriteString("# DJAlgoRhythm Configuration\n")
	content.WriteString("# =============================================================================\n")
	content.WriteString("#\n")
	content.WriteString("# Copy this file to .env and update with your values\n")
	content.WriteString("# All environment variables have CLI flag equivalents (use --help to see them)\n")
	content.WriteString("#\n")
	content.WriteString("# Format: DJALGORHYTHM_<SECTION>_<SETTING>=value\n")
	content.WriteString("# CLI equivalent: --<section>-<setting>\n")
	content.WriteString("#\n")
	content.WriteString("# =============================================================================\n")
	content.WriteString("# CHAT PLATFORMS - Choose one primary platform\n")
	content.WriteString("# =============================================================================\n\n")

	// Generate sections
	generateTelegramSection(&content, cmd)
	generateWhatsAppSection(&content, cmd)
	generateSpotifySection(&content, cmd)
	generateLLMSection(&content, cmd)
	generateAppSection(&content, cmd)
	generateServerSection(&content, cmd)
	generateLoggingSection(&content, cmd)
	generateQuickSetupGuide(&content)

	return content.String()
}

func flagToEnvVar(flagName string) string {
	return "DJALGORHYTHM_" + strings.ToUpper(strings.ReplaceAll(flagName, "-", "_"))
}

func getDefaultValueString(cmd *cobra.Command, flagName string) string {
	if f := cmd.PersistentFlags().Lookup(flagName); f != nil {
		return f.DefValue
	}
	return ""
}

func generateTelegramSection(content *strings.Builder, cmd *cobra.Command) {
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# Telegram Configuration (Recommended - Default enabled)\n")
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# CLI: --telegram-enabled, --telegram-bot-token, --telegram-group-id\n")

	// Get flag defaults
	enabledDefault := getDefaultValueString(cmd, "telegram-enabled")

	fmt.Fprintf(content, "%s=%s                           # Enable Telegram bot (default: %s)\n",
		flagToEnvVar("telegram-enabled"), enabledDefault, enabledDefault)
	fmt.Fprintf(content, "%s=123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11  # Bot token from @BotFather\n",
		flagToEnvVar("telegram-bot-token"))
	fmt.Fprintf(content, "%s=-100xxxxxxxxxx                # Group ID (get from @userinfobot)\n",
		flagToEnvVar("telegram-group-id"))
	content.WriteString("\n")
	content.WriteString("# Admin and Community Approval\n")
	content.WriteString("# CLI: --admin-needs-approval, --community-approval\n")

	adminDefault := getDefaultValueString(cmd, "admin-needs-approval")
	communityDefault := getDefaultValueString(cmd, "community-approval")

	fmt.Fprintf(content, "%s=false                           # Require admin approval for all songs (default: false)\n",
		flagToEnvVar("admin-approval"))
	fmt.Fprintf(content, "%s=%s                     # Require approval even from admins - for testing (default: %s)\n",
		flagToEnvVar("admin-needs-approval"), adminDefault, adminDefault)
	fmt.Fprintf(content, "%s=%s                           # Number of 👍 reactions to bypass admin approval, 0=disabled (default: %s)\n",
		flagToEnvVar("community-approval"), communityDefault, communityDefault)
	content.WriteString("\n")
}

func generateWhatsAppSection(content *strings.Builder, cmd *cobra.Command) {
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# WhatsApp Configuration (Optional - Disabled by default due to ToS concerns)\n")
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# CLI: --whatsapp-enabled, --whatsapp-group-jid, --whatsapp-device-name\n")

	enabledDefault := getDefaultValueString(cmd, "whatsapp-enabled")
	deviceDefault := getDefaultValueString(cmd, "whatsapp-device-name")

	fmt.Fprintf(content, "%s=%s                         # Enable WhatsApp integration (default: %s)\n",
		flagToEnvVar("whatsapp-enabled"), enabledDefault, enabledDefault)
	fmt.Fprintf(content, "%s=120363123456789@g.us        # WhatsApp group JID (use debug logging to find)\n",
		flagToEnvVar("whatsapp-group-jid"))
	fmt.Fprintf(content, "%s=\"%s\"           # Device name shown in WhatsApp (default: \"%s\")\n",
		flagToEnvVar("whatsapp-device-name"), deviceDefault, deviceDefault)
	fmt.Fprintf(content, "%s=\"./whatsapp_session.db\"  # Session file path (default: \"./whatsapp_session.db\")\n",
		flagToEnvVar("whatsapp-session-path"))
	content.WriteString("\n")
}

func generateSpotifySection(content *strings.Builder, _ *cobra.Command) {
	content.WriteString("# =============================================================================\n")
	content.WriteString("# SPOTIFY CONFIGURATION - Required\n")
	content.WriteString("# =============================================================================\n")
	content.WriteString("# Get these from https://developer.spotify.com/dashboard\n")
	content.WriteString("# CLI: --spotify-client-id, --spotify-client-secret, --spotify-playlist-id\n")
	content.WriteString("\n")

	fmt.Fprintf(content, "%s=your_spotify_client_id_here          # Spotify app client ID\n",
		flagToEnvVar("spotify-client-id"))
	fmt.Fprintf(content, "%s=your_spotify_client_secret_here  # Spotify app client secret\n",
		flagToEnvVar("spotify-client-secret"))
	fmt.Fprintf(content, "%s=your_target_playlist_id_here       # Target playlist ID (from Spotify URL)\n",
		flagToEnvVar("spotify-playlist-id"))
	fmt.Fprintf(content, "%s=http://127.0.0.1:8080/callback    # OAuth callback URL (default: auto-generated)\n",
		flagToEnvVar("spotify-redirect-url"))
	fmt.Fprintf(content, "%s=./spotify_token.json                # Token storage path (default: \"./spotify_token.json\")\n",
		flagToEnvVar("spotify-token-path"))
	content.WriteString("\n")
}

func generateLLMSection(content *strings.Builder, cmd *cobra.Command) {
	content.WriteString("# =============================================================================\n")
	content.WriteString("# AI/LLM CONFIGURATION - Optional but recommended for better song matching\n")
	content.WriteString("# =============================================================================\n")
	content.WriteString("\n")
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# LLM Provider Selection\n")
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# CLI: --llm-provider, --llm-api-key, --llm-model\n")

	providerDefault := getDefaultValueString(cmd, "llm-provider")

	fmt.Fprintf(content, "%s=%s                              # Provider: none, openai, anthropic, ollama (default: %s)\n",
		flagToEnvVar("llm-provider"), providerDefault, providerDefault)
	content.WriteString("\n")
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# OpenAI Configuration (Recommended for best results)\n")
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# Uncomment these lines and set DJALGORHYTHM_LLM_PROVIDER=openai\n")
	fmt.Fprintf(content, "# %s=sk-...                           # OpenAI API key\n",
		flagToEnvVar("llm-api-key"))
	fmt.Fprintf(content, "# %s=gpt-4o-mini                        # Model name (gpt-4o-mini is cost-effective)\n",
		flagToEnvVar("llm-model"))
	content.WriteString("\n")
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# Anthropic Configuration\n")
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# Uncomment these lines and set DJALGORHYTHM_LLM_PROVIDER=anthropic\n")
	fmt.Fprintf(content, "# %s=sk-ant-...                       # Anthropic API key\n",
		flagToEnvVar("llm-api-key"))
	fmt.Fprintf(content, "# %s=claude-3-haiku-20240307           # Model name (Haiku is fastest/cheapest)\n",
		flagToEnvVar("llm-model"))
	content.WriteString("\n")
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# Ollama Configuration (Local/Self-hosted)\n")
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# Uncomment these lines and set DJALGORHYTHM_LLM_PROVIDER=ollama\n")
	fmt.Fprintf(content, "# %s=http://localhost:11434          # Ollama server URL (default: http://localhost:11434)\n",
		flagToEnvVar("llm-base-url"))
	fmt.Fprintf(content, "# %s=llama3.2                          # Model name (must be installed in Ollama)\n",
		flagToEnvVar("llm-model"))
	content.WriteString("\n")
}

func generateAppSection(content *strings.Builder, cmd *cobra.Command) {
	content.WriteString("# =============================================================================\n")
	content.WriteString("# APPLICATION SETTINGS\n")
	content.WriteString("# =============================================================================\n")
	content.WriteString("\n")
	generateAppLocalizationSection(content, cmd)
	generateAppTimeoutsSection(content, cmd)
	generateAppQueueSection(content, cmd)
	generateAppShadowQueueSection(content, cmd)
	generateAppFloodPreventionSection(content, cmd)
}

func generateAppLocalizationSection(content *strings.Builder, cmd *cobra.Command) {
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# Localization\n")
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# CLI: --language\n")

	langDefault := getDefaultValueString(cmd, "language")
	supportedLangs := strings.Join(i18n.GetSupportedLanguages(), ", ")

	fmt.Fprintf(content, "%s=%s                                    # Bot language: %s (default: %s)\n",
		flagToEnvVar("language"), langDefault, supportedLangs, langDefault)
	content.WriteString("\n")
}

func generateAppTimeoutsSection(content *strings.Builder, cmd *cobra.Command) {
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# Timeouts and Retries (all values in seconds)\n")
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# CLI: --confirm-timeout-secs, --confirm-admin-timeout-secs, etc.\n")

	confirmDefault := getDefaultValueString(cmd, "confirm-timeout-secs")
	confirmAdminDefault := getDefaultValueString(cmd, "confirm-admin-timeout-secs")
	queueApprovalDefault := getDefaultValueString(cmd, "queue-track-approval-timeout-secs")
	maxReplacementsDefault := getDefaultValueString(cmd, "max-queue-track-replacements")

	fmt.Fprintf(content, "%s=%s                       # User confirmation timeout (default: %s)\n",
		flagToEnvVar("confirm-timeout-secs"), confirmDefault, confirmDefault)
	fmt.Fprintf(content, "%s=%s               # Admin confirmation timeout (default: %s)\n",
		flagToEnvVar("confirm-admin-timeout-secs"), confirmAdminDefault, confirmAdminDefault)
	fmt.Fprintf(content, "%s=%s          # Queue track approval timeout (default: %s)\n",
		flagToEnvVar("queue-track-approval-timeout-secs"), queueApprovalDefault, queueApprovalDefault)
	fmt.Fprintf(content, "%s=%s                # Max replacement attempts before auto-accept (default: %s)\n",
		flagToEnvVar("max-queue-track-replacements"), maxReplacementsDefault, maxReplacementsDefault)
	content.WriteString("\n")
}

func generateAppQueueSection(content *strings.Builder, cmd *cobra.Command) {
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# Queue Management - Ensures continuous playback\n")
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# CLI: --queue-ahead-duration-secs, --queue-check-interval-secs\n")

	queueAheadDefault := getDefaultValueString(cmd, "queue-ahead-duration-secs")
	queueCheckDefault := getDefaultValueString(cmd, "queue-check-interval-secs")

	fmt.Fprintf(content, "%s=%s                  # Target queue duration ahead of current song (default: %s)\n",
		flagToEnvVar("queue-ahead-duration-secs"), queueAheadDefault, queueAheadDefault)
	fmt.Fprintf(content, "%s=%s                  # How often to check queue status (default: %s)\n",
		flagToEnvVar("queue-check-interval-secs"), queueCheckDefault, queueCheckDefault)
	fmt.Fprintf(content, "%s=30         # Warning timeout for queue sync issues (default: 30)\n",
		flagToEnvVar("queue-sync-warning-timeout-minutes"))
	content.WriteString("\n")
}

func generateAppShadowQueueSection(content *strings.Builder, cmd *cobra.Command) {
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# Shadow Queue - Maintains reliable queue state tracking\n")
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# CLI: --shadow-queue-maintenance-interval-mins, --shadow-queue-max-age-hours\n")

	shadowMaintenanceDefault := getDefaultValueString(cmd, "shadow-queue-maintenance-interval-mins")
	shadowMaxAgeDefault := getDefaultValueString(cmd, "shadow-queue-max-age-hours")

	fmt.Fprintf(content, "%s=30     # Maintenance interval in seconds (CLI uses minutes!) (default: converted from %s mins)\n",
		flagToEnvVar("shadow-queue-maintenance-interval-secs"), shadowMaintenanceDefault)
	fmt.Fprintf(content, "%s=%s                  # Max age of shadow queue items (default: %s)\n",
		flagToEnvVar("shadow-queue-max-age-hours"), shadowMaxAgeDefault, shadowMaxAgeDefault)
	content.WriteString("\n")
}

func generateAppFloodPreventionSection(content *strings.Builder, cmd *cobra.Command) {
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# Flood Prevention - Anti-spam protection\n")
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# CLI: --flood-limit-per-minute\n")

	floodDefault := getDefaultValueString(cmd, "flood-limit-per-minute")

	fmt.Fprintf(content, "%s=%s                      # Max messages per user per minute (default: %s)\n",
		flagToEnvVar("flood-limit-per-minute"), floodDefault, floodDefault)
	content.WriteString("\n")
}

func generateServerSection(content *strings.Builder, cmd *cobra.Command) {
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# HTTP Server Configuration\n")
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# CLI: --server-host, --server-port\n")

	hostDefault := getDefaultValueString(cmd, "server-host")
	portDefault := getDefaultValueString(cmd, "server-port")

	fmt.Fprintf(content, "%s=%s                         # Server bind address (default: %s)\n",
		flagToEnvVar("server-host"), "127.0.0.1", hostDefault)
	fmt.Fprintf(content, "%s=%s                              # Server port (default: %s)\n",
		flagToEnvVar("server-port"), portDefault, portDefault)
	content.WriteString("\n")
}

func generateLoggingSection(content *strings.Builder, cmd *cobra.Command) {
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# Logging Configuration\n")
	content.WriteString("# -----------------------------------------------------------------------------\n")
	content.WriteString("# CLI: --log-level\n")

	logDefault := getDefaultValueString(cmd, "log-level")

	fmt.Fprintf(content, "%s=%s                                # Log level: debug, info, warn, error (default: %s)\n",
		flagToEnvVar("log-level"), logDefault, logDefault)
	fmt.Fprintf(content, "%s=json                               # Log format: json, text (default: json)\n",
		flagToEnvVar("log-format"))
	content.WriteString("\n")
}

func generateQuickSetupGuide(content *strings.Builder) {
	generateSetupGuideHeader(content)
	generateSetupSteps(content)
	generateTroubleshootingSection(content)
}

func generateSetupGuideHeader(content *strings.Builder) {
	content.WriteString("# =============================================================================\n")
	content.WriteString("# QUICK SETUP GUIDE\n")
	content.WriteString("# =============================================================================\n")
	content.WriteString("\n")
}

func generateSetupSteps(content *strings.Builder) {
	content.WriteString("# 1. TELEGRAM SETUP (Recommended):\n")
	content.WriteString("#    - Message @BotFather on Telegram\n")
	content.WriteString("#    - Create bot with /newbot command\n")
	content.WriteString("#    - Copy bot token to DJALGORHYTHM_TELEGRAM_BOT_TOKEN above\n")
	content.WriteString("#    - Add bot to your group as admin\n")
	content.WriteString("#    - Get group ID with @userinfobot and set DJALGORHYTHM_TELEGRAM_GROUP_ID above\n")
	content.WriteString("#    - Optional: Enable admin approval by setting DJALGORHYTHM_ADMIN_APPROVAL=true\n")
	content.WriteString("\n")
	content.WriteString("# 2. SPOTIFY SETUP (Required):\n")
	content.WriteString("#    - Go to https://developer.spotify.com/dashboard\n")
	content.WriteString("#    - Create new app with name \"DJAlgoRhythm\"\n")
	content.WriteString("#    - Add redirect URI: http://127.0.0.1:8080/callback\n")
	content.WriteString("#    - Copy Client ID and Secret to config above\n")
	content.WriteString("#    - Get target playlist ID from Spotify URL (the part after /playlist/)\n")
	content.WriteString("#    - Make sure playlist is public or owned by the authenticating user\n")
	content.WriteString("\n")
	content.WriteString("# 3. LLM SETUP (Optional but recommended):\n")
	content.WriteString("#    - For OpenAI: Get API key from https://platform.openai.com/api-keys\n")
	content.WriteString("#    - For Anthropic: Get API key from https://console.anthropic.com/\n")
	content.WriteString("#    - For Ollama: Install locally and run `ollama pull llama3.2`\n")
	content.WriteString("#    - Set provider and credentials above\n")
	content.WriteString("\n")
	content.WriteString("# 4. TEST CONFIGURATION:\n")
	content.WriteString("#    go run ./cmd/djalgorhythm --help                        # See all CLI options\n")
	content.WriteString("#    go run ./cmd/djalgorhythm --log-level=debug            # Run with debug logging\n")
	content.WriteString("#    make build && ./bin/djalgorhythm                       # Build and run\n")
	content.WriteString("\n")
}

func generateTroubleshootingSection(content *strings.Builder) {
	content.WriteString("# =============================================================================\n")
	content.WriteString("# TROUBLESHOOTING\n")
	content.WriteString("# =============================================================================\n")
	content.WriteString("\n")
	content.WriteString("# Issue: \"Bot doesn't respond to messages\"\n")
	content.WriteString("# - Check DJALGORHYTHM_TELEGRAM_GROUP_ID is correct (negative number for groups)\n")
	content.WriteString("# - Ensure bot is admin in the group\n")
	content.WriteString("# - Check bot token is valid with curl: curl https://api.telegram.org/bot<TOKEN>/getMe\n")
	content.WriteString("\n")
	content.WriteString("# Issue: \"Spotify authentication fails\"\n")
	content.WriteString("# - Verify redirect URL in Spotify app matches DJALGORHYTHM_SPOTIFY_REDIRECT_URL\n")
	content.WriteString("# - Check client ID and secret are correct\n")
	content.WriteString("# - Ensure playlist exists and is accessible\n")
	content.WriteString("\n")
	content.WriteString("# Issue: \"LLM not working\"\n")
	content.WriteString("# - Check API key is valid for chosen provider\n")
	content.WriteString("# - For Ollama: ensure model is pulled with `ollama pull <model>`\n")
	content.WriteString("# - Check rate limits and quotas for paid providers\n")
	content.WriteString("\n")
	content.WriteString("# Issue: \"Songs not being added\"\n")
	content.WriteString("# - Check playlist permissions (must be public or owned by auth user)\n")
	content.WriteString("# - Verify Spotify Premium account (required for queue manipulation)\n")
	content.WriteString("# - Check logs with DJALGORHYTHM_LOG_LEVEL=debug\n")
}
