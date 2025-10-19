// Package telegram provides Telegram Bot API integration using go-telegram/bot library.
package telegram

import (
	"context"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.uber.org/zap"

	"whatdj/internal/chat"
	"whatdj/internal/i18n"
	"whatdj/pkg/text"
)

const (
	entityTypeURL         = "url"
	chatTypeGroup         = "group"
	chatTypeSuperGroup    = "supergroup"
	groupDiscoveryTimeout = 15 // seconds for group discovery
	// Sleep durations for group discovery
	botStopDelay       = 200 * time.Millisecond
	discoveryFinalWait = 50 * time.Millisecond
)

// Config holds Telegram-specific configuration
type Config struct {
	BotToken           string
	GroupID            int64 // Chat ID of the group to monitor
	Enabled            bool
	ReactionSupport    bool   // Whether the group supports reactions
	AdminApproval      bool   // Whether admin approval is required for songs
	AdminNeedsApproval bool   // Whether admins also need approval (for testing)
	CommunityApproval  int    // Number of 👍 reactions needed to bypass admin approval (0 disables)
	Language           string // Bot language for user-facing messages
}

// Frontend implements the chat.Frontend interface for Telegram
type Frontend struct {
	config    *Config
	logger    *zap.Logger
	bot       *bot.Bot
	parser    *text.Parser
	localizer *i18n.Localizer

	// Message handling
	messageHandler func(*chat.Message)

	// Auto-play decision handling
	autoPlayDecisionHandler func(trackID string, approved bool)

	// Playlist switch decision handling
	playlistSwitchDecisionHandler func(approved bool)

	// Approval tracking
	approvalMutex    sync.RWMutex
	pendingApprovals map[string]*approvalContext

	// Admin approval tracking
	adminApprovalMutex    sync.RWMutex
	pendingAdminApprovals map[string]*adminApprovalContext

	// Community approval tracking
	communityApprovalMutex    sync.RWMutex
	pendingCommunityApprovals map[string]*communityApprovalContext
}

// approvalContext tracks pending user approvals
type approvalContext struct {
	originUserID int64
	approved     chan bool
	cancelCtx    context.Context
	cancelFunc   context.CancelFunc
}

// adminApprovalContext tracks pending admin approvals
type adminApprovalContext struct {
	originUserID   int64
	originUserName string
	songInfo       string
	songURL        string
	approved       chan bool
	cancelCtx      context.Context
	cancelFunc     context.CancelFunc
	sentToAdmins   []int64
}

// communityApprovalContext tracks pending community approvals via reactions
type communityApprovalContext struct {
	messageID         int
	requiredReactions int
	currentReactions  int
	reactedUsers      map[int64]bool // track users who reacted to prevent double counting
	approved          chan bool
	cancelCtx         context.Context
	cancelFunc        context.CancelFunc
}

// NewFrontend creates a new Telegram frontend
func NewFrontend(config *Config, logger *zap.Logger) *Frontend {
	// Use configured language, fallback to default if not set
	language := config.Language
	if language == "" {
		language = i18n.DefaultLanguage
	}

	return &Frontend{
		config:                    config,
		logger:                    logger,
		parser:                    text.NewParser(),
		localizer:                 i18n.NewLocalizer(language),
		pendingApprovals:          make(map[string]*approvalContext),
		pendingAdminApprovals:     make(map[string]*adminApprovalContext),
		pendingCommunityApprovals: make(map[string]*communityApprovalContext),
	}
}

// Start initializes the Telegram bot and begins listening for updates
func (f *Frontend) Start(ctx context.Context) error {
	if !f.config.Enabled {
		f.logger.Info("Telegram frontend is disabled, skipping initialization")
		return nil
	}

	f.logger.Info("Starting Telegram frontend",
		zap.String("group_id", fmt.Sprintf("%d", f.config.GroupID)))

	opts := []bot.Option{
		bot.WithDefaultHandler(f.handleUpdate),
		bot.WithCallbackQueryDataHandler("confirm_", bot.MatchTypePrefix, f.handleConfirmCallback),
		bot.WithCallbackQueryDataHandler("reject_", bot.MatchTypePrefix, f.handleRejectCallback),
		bot.WithCallbackQueryDataHandler("admin_approve_", bot.MatchTypePrefix, f.handleAdminApproveCallback),
		bot.WithCallbackQueryDataHandler("admin_deny_", bot.MatchTypePrefix, f.handleAdminDenyCallback),
		bot.WithCallbackQueryDataHandler("autoplay_approve_", bot.MatchTypePrefix, f.handleAutoPlayApproveCallback),
		bot.WithCallbackQueryDataHandler("autoplay_deny_", bot.MatchTypePrefix, f.handleAutoPlayDenyCallback),
		bot.WithCallbackQueryDataHandler("playlist_switch_approve", bot.MatchTypeExact, f.handlePlaylistSwitchApproveCallback),
		bot.WithCallbackQueryDataHandler("playlist_switch_deny", bot.MatchTypeExact, f.handlePlaylistSwitchDenyCallback),
	}

	b, err := bot.New(f.config.BotToken, opts...)
	if err != nil {
		return fmt.Errorf("failed to create telegram bot: %w", err)
	}

	f.bot = b

	// Verify bot can access the group (skip if GroupID is 0 for interactive setup)
	if f.config.GroupID != 0 {
		if err := f.verifyGroupAccess(ctx); err != nil {
			return fmt.Errorf("failed to verify group access: %w", err)
		}
	}

	f.logger.Info("Telegram frontend started successfully")
	return nil
}

// Listen starts listening for messages and calls the handler for each message
func (f *Frontend) Listen(ctx context.Context, handler func(*chat.Message)) error {
	if !f.config.Enabled {
		return nil // Do nothing if disabled
	}

	f.messageHandler = handler

	// Start the bot
	f.bot.Start(ctx)

	return nil
}

// SendText sends a text message to the specified chat, optionally as a reply
func (f *Frontend) SendText(ctx context.Context, chatID, replyToID, text string) (string, error) {
	if !f.config.Enabled {
		return "", fmt.Errorf("telegram frontend is disabled")
	}

	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid chat ID: %w", err)
	}

	params := &bot.SendMessageParams{
		ChatID: chatIDInt,
		Text:   text,
	}

	// Disable link previews for all messages since the bot primarily sends Spotify links
	// which don't work well with Telegram's preview system
	disabled := true
	params.LinkPreviewOptions = &models.LinkPreviewOptions{
		IsDisabled: &disabled,
	}

	if replyToID != "" {
		messageID, parseErr := strconv.Atoi(replyToID)
		if parseErr != nil {
			return "", fmt.Errorf("invalid reply message ID: %w", parseErr)
		}
		params.ReplyParameters = &models.ReplyParameters{
			MessageID: messageID,
		}
	}

	msg, err := f.bot.SendMessage(ctx, params)
	if err != nil {
		return "", fmt.Errorf("failed to send message: %w", err)
	}

	return strconv.Itoa(msg.ID), nil
}

// DeleteMessage deletes a message by its ID
func (f *Frontend) DeleteMessage(ctx context.Context, chatID, msgID string) error {
	if !f.config.Enabled {
		return fmt.Errorf("telegram frontend is disabled")
	}

	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat ID: %w", err)
	}

	messageID, err := strconv.Atoi(msgID)
	if err != nil {
		return fmt.Errorf("invalid message ID: %w", err)
	}

	params := &bot.DeleteMessageParams{
		ChatID:    chatIDInt,
		MessageID: messageID,
	}

	_, err = f.bot.DeleteMessage(ctx, params)
	if err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}

	return nil
}

// React adds an emoji reaction to a message
func (f *Frontend) React(ctx context.Context, chatID, msgID string, r chat.Reaction) error {
	if !f.config.Enabled {
		return fmt.Errorf("telegram frontend is disabled")
	}

	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat ID: %w", err)
	}

	messageID, err := strconv.Atoi(msgID)
	if err != nil {
		return fmt.Errorf("invalid message ID: %w", err)
	}

	// Try to set reaction first
	_, err = f.bot.SetMessageReaction(ctx, &bot.SetMessageReactionParams{
		ChatID:    chatIDInt,
		MessageID: messageID,
		Reaction: []models.ReactionType{
			{
				Type: models.ReactionTypeTypeEmoji,
				ReactionTypeEmoji: &models.ReactionTypeEmoji{
					Emoji: string(r),
				},
			},
		},
	})

	if err != nil {
		f.logger.Debug("Failed to set reaction, reactions may not be supported",
			zap.Error(err))
		// Reactions not supported, this is OK - we'll handle approval via inline keyboards
		return nil
	}

	return nil
}

// AwaitApproval waits for user approval via reaction or inline buttons
func (f *Frontend) AwaitApproval(ctx context.Context, origin *chat.Message, prompt string, timeoutSec int) (bool, error) {
	if !f.config.Enabled {
		return false, fmt.Errorf("telegram frontend is disabled")
	}

	chatIDInt, err := strconv.ParseInt(origin.ChatID, 10, 64)
	if err != nil {
		return false, fmt.Errorf("invalid chat ID: %w", err)
	}

	originalUserID, err := strconv.ParseInt(origin.SenderID, 10, 64)
	if err != nil {
		return false, fmt.Errorf("invalid sender ID: %w", err)
	}

	// Create approval context
	approvalCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	approval := &approvalContext{
		originUserID: originalUserID,
		approved:     make(chan bool, 1),
		cancelCtx:    approvalCtx,
		cancelFunc:   cancel,
	}

	// Generate unique key for this approval
	approvalKey := fmt.Sprintf("%s_%s_%d", origin.ChatID, origin.ID, time.Now().Unix())

	f.approvalMutex.Lock()
	f.pendingApprovals[approvalKey] = approval
	f.approvalMutex.Unlock()

	// Cleanup function
	defer func() {
		cancel()
		f.approvalMutex.Lock()
		delete(f.pendingApprovals, approvalKey)
		f.approvalMutex.Unlock()
	}()

	// Try reactions first, then fallback to inline keyboard
	originalMsgID, _ := strconv.Atoi(origin.ID)

	// Send prompt message with inline keyboard as primary method
	keyboard := [][]models.InlineKeyboardButton{
		{
			{
				Text:         f.localizer.T("button.confirm"),
				CallbackData: "confirm_" + approvalKey,
			},
			{
				Text:         f.localizer.T("button.not_this"),
				CallbackData: "reject_" + approvalKey,
			},
		},
	}

	params := &bot.SendMessageParams{
		ChatID:      chatIDInt,
		Text:        prompt,
		ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: keyboard},
		ReplyParameters: &models.ReplyParameters{
			MessageID: originalMsgID,
		},
	}

	// Disable link previews for approval prompts
	disabled := true
	params.LinkPreviewOptions = &models.LinkPreviewOptions{
		IsDisabled: &disabled,
	}

	promptMsg, err := f.bot.SendMessage(ctx, params)
	if err != nil {
		return false, fmt.Errorf("failed to send approval prompt: %w", err)
	}

	// Store the prompt message ID for cleanup
	promptMsgID := promptMsg.ID

	// Wait for approval or timeout
	select {
	case approved := <-approval.approved:
		// Clean up the prompt message
		if delErr := f.DeleteMessage(ctx, strconv.FormatInt(chatIDInt, 10), strconv.Itoa(promptMsgID)); delErr != nil {
			f.logger.Debug("Failed to delete prompt message", zap.Error(delErr))
		}
		return approved, nil
	case <-approvalCtx.Done():
		// Clean up the prompt message on timeout
		if delErr := f.DeleteMessage(ctx, strconv.FormatInt(chatIDInt, 10), strconv.Itoa(promptMsgID)); delErr != nil {
			f.logger.Debug("Failed to delete prompt message on timeout", zap.Error(delErr))
		}
		return false, nil
	}
}

// handleUpdate processes incoming Telegram updates
func (f *Frontend) handleUpdate(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if update.Message != nil {
		f.handleMessage(ctx, update.Message)
	}
	if update.MessageReactionCount != nil {
		f.handleMessageReactionCount(ctx, update.MessageReactionCount)
	}
}

// handleMessage processes incoming messages
func (f *Frontend) handleMessage(_ context.Context, msg *models.Message) {
	// Only process messages from the configured group
	if msg.Chat.ID != f.config.GroupID {
		return
	}

	// Ignore messages from the bot itself
	if msg.From.IsBot {
		return
	}

	// Extract URLs from the message
	urls := f.extractURLs(msg)

	// Convert to unified message format
	message := chat.Message{
		ID:         strconv.Itoa(msg.ID),
		ChatID:     strconv.FormatInt(msg.Chat.ID, 10),
		SenderID:   strconv.FormatInt(msg.From.ID, 10),
		SenderName: f.getUserDisplayName(msg.From),
		Text:       msg.Text,
		URLs:       urls,
		IsGroup:    msg.Chat.Type == chatTypeGroup || msg.Chat.Type == chatTypeSuperGroup,
		Raw:        msg,
	}

	// Call the message handler
	if f.messageHandler != nil {
		f.messageHandler(&message)
	}
}

// handleMessageReactionCount processes incoming message reaction count updates for community approval
func (f *Frontend) handleMessageReactionCount(_ context.Context, reactionCount *models.MessageReactionCountUpdated) {
	// Only process reactions from the configured group
	if reactionCount.Chat.ID != f.config.GroupID {
		return
	}

	// Check if there are any pending community approvals for this message
	f.communityApprovalMutex.Lock()
	defer f.communityApprovalMutex.Unlock()

	for _, approval := range f.pendingCommunityApprovals {
		if approval.messageID == reactionCount.MessageID {
			f.processReactionCountForCommunityApproval(approval, reactionCount)
			break
		}
	}
}

// processReactionCountForCommunityApproval processes a reaction count update for community approval
func (f *Frontend) processReactionCountForCommunityApproval(
	approval *communityApprovalContext, reactionCount *models.MessageReactionCountUpdated) {
	// Count 👍 reactions
	thumbsUpCount := 0

	for _, reaction := range reactionCount.Reactions {
		if reaction.Type.Type == models.ReactionTypeTypeEmoji &&
			reaction.Type.ReactionTypeEmoji != nil &&
			reaction.Type.ReactionTypeEmoji.Emoji == "👍" {
			thumbsUpCount = reaction.TotalCount
			break
		}
	}

	// Adjust for bot's initial reaction: subtract 1 since the bot adds a 👍 when creating the message
	// We want to count only user reactions for community approval
	userReactions := thumbsUpCount
	if thumbsUpCount > 0 {
		userReactions = thumbsUpCount - 1 // Exclude bot's initial reaction
	}

	// Update the approval context with user reactions (excluding bot)
	approval.currentReactions = userReactions

	f.logger.Debug("Community approval reaction count update",
		zap.Int("message_id", approval.messageID),
		zap.Int("total_reactions", thumbsUpCount),
		zap.Int("user_reactions", userReactions),
		zap.Int("required_reactions", approval.requiredReactions))

	// Check if we've reached the required number of user reactions
	if userReactions >= approval.requiredReactions {
		select {
		case approval.approved <- true:
			f.logger.Info("Community approval achieved via reactions",
				zap.Int("message_id", approval.messageID),
				zap.Int("user_reactions_received", userReactions),
				zap.Int("total_reactions_received", thumbsUpCount),
				zap.Int("reactions_required", approval.requiredReactions))
		case <-approval.cancelCtx.Done():
			// Context already canceled, do nothing
		}
	}
}

// handleConfirmCallback handles confirmation button clicks
func (f *Frontend) handleConfirmCallback(ctx context.Context, b *bot.Bot, update *models.Update) {
	f.handleApprovalCallback(ctx, b, update, true)
}

// handleRejectCallback handles rejection button clicks
func (f *Frontend) handleRejectCallback(ctx context.Context, b *bot.Bot, update *models.Update) {
	f.handleApprovalCallback(ctx, b, update, false)
}

// handleApprovalCallback handles both confirm and reject callbacks
func (f *Frontend) handleApprovalCallback(ctx context.Context, b *bot.Bot, update *models.Update, approved bool) {
	if update.CallbackQuery == nil {
		return
	}

	callbackData := update.CallbackQuery.Data

	// Extract approval key
	var approvalKey string
	if approved && strings.HasPrefix(callbackData, "confirm_") {
		approvalKey = strings.TrimPrefix(callbackData, "confirm_")
	} else if !approved && strings.HasPrefix(callbackData, "reject_") {
		approvalKey = strings.TrimPrefix(callbackData, "reject_")
	} else {
		return
	}

	f.approvalMutex.RLock()
	approval, exists := f.pendingApprovals[approvalKey]
	f.approvalMutex.RUnlock()

	if !exists {
		// Approval context not found or expired
		if _, ansErr := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            f.localizer.T("callback.prompt_expired"),
		}); ansErr != nil {
			f.logger.Debug("Failed to answer callback query", zap.Error(ansErr))
		}
		return
	}

	// Check if the user clicking is the same as the original sender
	if update.CallbackQuery.From.ID != approval.originUserID {
		if _, ansErr := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            f.localizer.T("callback.sender_only"),
		}); ansErr != nil {
			f.logger.Debug("Failed to answer callback query", zap.Error(ansErr))
		}
		return
	}

	// Send approval result
	select {
	case approval.approved <- approved:
		var responseText string
		if approved {
			responseText = f.localizer.T("callback.approved")
		} else {
			responseText = f.localizer.T("callback.denied")
		}

		if _, ansErr := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            responseText,
		}); ansErr != nil {
			f.logger.Debug("Failed to answer callback query", zap.Error(ansErr))
		}
	case <-approval.cancelCtx.Done():
		if _, ansErr := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            f.localizer.T("callback.prompt_expired"),
		}); ansErr != nil {
			f.logger.Debug("Failed to answer callback query", zap.Error(ansErr))
		}
	}
}

// verifyGroupAccess checks if the bot has access to the configured group
func (f *Frontend) verifyGroupAccess(ctx context.Context) error {
	chat, err := f.bot.GetChat(ctx, &bot.GetChatParams{
		ChatID: f.config.GroupID,
	})
	if err != nil {
		return fmt.Errorf("cannot access group %d: %w", f.config.GroupID, err)
	}

	f.logger.Info("Bot has access to group",
		zap.String("group_title", chat.Title),
		zap.String("group_type", string(chat.Type)))

	return nil
}

// extractURLs extracts URLs from message entities
func (f *Frontend) extractURLs(msg *models.Message) []string {
	var urls []string

	if msg.Entities != nil {
		for _, entity := range msg.Entities {
			if entity.Type == entityTypeURL {
				url := msg.Text[entity.Offset : entity.Offset+entity.Length]
				urls = append(urls, url)
			}
		}
	}

	return urls
}

// getUserDisplayName creates a display name for the user
func (f *Frontend) getUserDisplayName(user *models.User) string {
	if user.Username != "" {
		return "@" + user.Username
	}

	name := user.FirstName
	if user.LastName != "" {
		name += " " + user.LastName
	}

	return name
}

// IsAdminApprovalEnabled returns whether admin approval is enabled
func (f *Frontend) IsAdminApprovalEnabled() bool {
	return f.config.AdminApproval && f.config.Enabled
}

// GetGroupAdmins returns a list of admin user IDs for the configured group
func (f *Frontend) GetGroupAdmins(ctx context.Context) ([]int64, error) {
	if !f.config.Enabled {
		return nil, fmt.Errorf("telegram frontend is disabled")
	}

	admins, err := f.bot.GetChatAdministrators(ctx, &bot.GetChatAdministratorsParams{
		ChatID: f.config.GroupID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get chat administrators: %w", err)
	}

	var adminIDs []int64
	for _, admin := range admins {
		var user *models.User

		// Extract user based on the chat member type
		switch admin.Type {
		case models.ChatMemberTypeOwner:
			if admin.Owner != nil && admin.Owner.User != nil {
				user = admin.Owner.User
			}
		case models.ChatMemberTypeAdministrator:
			if admin.Administrator != nil {
				user = &admin.Administrator.User
			}
		case models.ChatMemberTypeMember, models.ChatMemberTypeRestricted,
			models.ChatMemberTypeLeft, models.ChatMemberTypeBanned:
			// These are not admin types, skip
			continue
		}

		// Skip bots from admin list
		if user != nil && !user.IsBot {
			adminIDs = append(adminIDs, user.ID)
		}
	}

	f.logger.Debug("Retrieved group admins",
		zap.Int("count", len(adminIDs)),
		zap.Int64s("admin_ids", adminIDs))

	return adminIDs, nil
}

// AwaitAdminApproval requests approval from group administrators
func (f *Frontend) AwaitAdminApproval(ctx context.Context, origin *chat.Message, songInfo, songURL string, timeoutSec int) (bool, error) {
	if !f.config.Enabled {
		return false, fmt.Errorf("telegram frontend is disabled")
	}

	// Get group administrators
	adminIDs, err := f.GetGroupAdmins(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get group admins: %w", err)
	}

	if len(adminIDs) == 0 {
		f.logger.Warn("No group administrators found, auto-approving")
		return true, nil
	}

	// Create admin approval context
	approvalCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	adminApproval := &adminApprovalContext{
		originUserID:   func() int64 { id, _ := strconv.ParseInt(origin.SenderID, 10, 64); return id }(),
		originUserName: origin.SenderName,
		songInfo:       songInfo,
		songURL:        songURL,
		approved:       make(chan bool, 1),
		cancelCtx:      approvalCtx,
		cancelFunc:     cancel,
		sentToAdmins:   adminIDs,
	}

	// Generate unique key for this admin approval
	approvalKey := fmt.Sprintf("admin_%s_%s_%d", origin.ChatID, origin.ID, time.Now().Unix())

	f.adminApprovalMutex.Lock()
	f.pendingAdminApprovals[approvalKey] = adminApproval
	f.adminApprovalMutex.Unlock()

	// Cleanup function
	defer func() {
		cancel()
		f.adminApprovalMutex.Lock()
		delete(f.pendingAdminApprovals, approvalKey)
		f.adminApprovalMutex.Unlock()
	}()

	// Send approval request to all admins
	if err := f.sendAdminApprovalRequests(ctx, adminIDs, approvalKey, adminApproval); err != nil {
		return false, fmt.Errorf("failed to send admin approval requests: %w", err)
	}

	// Wait for approval or timeout
	select {
	case approved := <-adminApproval.approved:
		return approved, nil
	case <-approvalCtx.Done():
		f.logger.Info("Admin approval timed out, denying by default",
			zap.String("approval_key", approvalKey))
		return false, nil
	}
}

// sendAdminApprovalRequests sends DM approval requests to all group admins
func (f *Frontend) sendAdminApprovalRequests(ctx context.Context, adminIDs []int64,
	approvalKey string, approval *adminApprovalContext) error {
	prompt := f.localizer.T("admin.approval_prompt",
		approval.originUserName,
		approval.songInfo,
		approval.songURL)

	keyboard := [][]models.InlineKeyboardButton{
		{
			{
				Text:         f.localizer.T("admin.button_approve"),
				CallbackData: "admin_approve_" + approvalKey,
			},
			{
				Text:         f.localizer.T("admin.button_deny"),
				CallbackData: "admin_deny_" + approvalKey,
			},
		},
	}

	var errors []error
	successCount := 0

	for _, adminID := range adminIDs {
		params := &bot.SendMessageParams{
			ChatID:      adminID,
			Text:        prompt,
			ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: keyboard},
		}

		// Disable link previews for admin approval messages
		disabled := true
		params.LinkPreviewOptions = &models.LinkPreviewOptions{
			IsDisabled: &disabled,
		}

		_, err := f.bot.SendMessage(ctx, params)
		if err != nil {
			f.logger.Warn("Failed to send admin approval request",
				zap.Int64("admin_id", adminID),
				zap.Error(err))
			errors = append(errors, err)
		} else {
			successCount++
			f.logger.Debug("Sent admin approval request",
				zap.Int64("admin_id", adminID),
				zap.String("approval_key", approvalKey))
		}
	}

	if successCount == 0 {
		return fmt.Errorf("failed to send approval request to any admin")
	}

	f.logger.Info("Sent admin approval requests",
		zap.Int("total_admins", len(adminIDs)),
		zap.Int("successful", successCount),
		zap.Int("failed", len(errors)))

	return nil
}

// handleAdminApproveCallback handles admin approval button clicks
func (f *Frontend) handleAdminApproveCallback(ctx context.Context, b *bot.Bot, update *models.Update) {
	f.handleAdminApprovalCallback(ctx, b, update, true)
}

// handleAdminDenyCallback handles admin denial button clicks
func (f *Frontend) handleAdminDenyCallback(ctx context.Context, b *bot.Bot, update *models.Update) {
	f.handleAdminApprovalCallback(ctx, b, update, false)
}

// handleAdminApprovalCallback handles both admin approve and deny callbacks
func (f *Frontend) handleAdminApprovalCallback(ctx context.Context, b *bot.Bot, update *models.Update, approved bool) {
	if update.CallbackQuery == nil {
		return
	}

	approvalKey := f.extractAdminApprovalKey(update.CallbackQuery.Data, approved)
	if approvalKey == "" {
		return
	}

	approval := f.getAdminApproval(approvalKey)
	if approval == nil {
		f.answerExpiredCallback(ctx, b, update.CallbackQuery.ID)
		return
	}

	if !f.isUserAdmin(update.CallbackQuery.From.ID, approval.sentToAdmins) {
		f.answerUnauthorizedCallback(ctx, b, update.CallbackQuery.ID)
		return
	}

	f.processAdminDecision(ctx, b, update, approval, approved)
}

func (f *Frontend) extractAdminApprovalKey(callbackData string, approved bool) string {
	if approved && strings.HasPrefix(callbackData, "admin_approve_") {
		return strings.TrimPrefix(callbackData, "admin_approve_")
	}
	if !approved && strings.HasPrefix(callbackData, "admin_deny_") {
		return strings.TrimPrefix(callbackData, "admin_deny_")
	}
	return ""
}

// handleAutoPlayApproveCallback handles auto-play approval button clicks
func (f *Frontend) handleAutoPlayApproveCallback(ctx context.Context, b *bot.Bot, update *models.Update) {
	f.handleAutoPlayCallback(ctx, b, update, true)
}

// handleAutoPlayDenyCallback handles auto-play denial button clicks
func (f *Frontend) handleAutoPlayDenyCallback(ctx context.Context, b *bot.Bot, update *models.Update) {
	f.handleAutoPlayCallback(ctx, b, update, false)
}

// handleAutoPlayCallback handles both auto-play approve and deny callbacks
func (f *Frontend) handleAutoPlayCallback(ctx context.Context, b *bot.Bot, update *models.Update, approved bool) {
	if update.CallbackQuery == nil {
		return
	}

	trackID := f.extractAutoPlayTrackID(update.CallbackQuery.Data, approved)
	if trackID == "" {
		return
	}

	// Answer the callback query immediately
	var responseText string
	if approved {
		responseText = f.localizer.T("callback.autoplay_approved")
	} else {
		responseText = f.localizer.T("callback.autoplay_denied")
	}

	if _, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
		Text:            responseText,
	}); err != nil {
		f.logger.Debug("Failed to answer callback query", zap.Error(err))
	}

	if approved {
		// React with thumbs up emoji to show approval
		if err := f.React(ctx, strconv.FormatInt(update.CallbackQuery.Message.Message.Chat.ID, 10),
			strconv.Itoa(update.CallbackQuery.Message.Message.ID), chat.ReactionThumbsUp); err != nil {
			f.logger.Debug("Failed to add thumbs up reaction to auto-play message", zap.Error(err))
		}

		// Remove the inline keyboard by editing the message
		if _, err := b.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{
			ChatID:    update.CallbackQuery.Message.Message.Chat.ID,
			MessageID: update.CallbackQuery.Message.Message.ID,
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{},
			},
		}); err != nil {
			f.logger.Debug("Failed to remove buttons from auto-play message", zap.Error(err))
		}
	} else {
		// Delete the message on denial
		if _, err := b.DeleteMessage(ctx, &bot.DeleteMessageParams{
			ChatID:    update.CallbackQuery.Message.Message.Chat.ID,
			MessageID: update.CallbackQuery.Message.Message.ID,
		}); err != nil {
			f.logger.Debug("Failed to delete auto-play message", zap.Error(err))
		}
	}

	// Notify the dispatcher about the decision
	if f.autoPlayDecisionHandler != nil {
		f.autoPlayDecisionHandler(trackID, approved)
	}

	f.logger.Info("Auto-play decision processed",
		zap.String("trackID", trackID),
		zap.Bool("approved", approved),
		zap.String("userID", strconv.FormatInt(update.CallbackQuery.From.ID, 10)))
}

// handlePlaylistSwitchApproveCallback handles playlist switch approval
func (f *Frontend) handlePlaylistSwitchApproveCallback(ctx context.Context, b *bot.Bot, update *models.Update) {
	f.handlePlaylistSwitchCallback(ctx, b, update, true)
}

// handlePlaylistSwitchDenyCallback handles playlist switch denial
func (f *Frontend) handlePlaylistSwitchDenyCallback(ctx context.Context, b *bot.Bot, update *models.Update) {
	f.handlePlaylistSwitchCallback(ctx, b, update, false)
}

// handlePlaylistSwitchCallback handles both playlist switch approve and deny callbacks
func (f *Frontend) handlePlaylistSwitchCallback(ctx context.Context, b *bot.Bot, update *models.Update, approved bool) {
	if update.CallbackQuery == nil {
		return
	}

	// Note: We don't check admin status here because playlist switch messages are only sent to admins
	// and they might be responding from a DM where we can't verify group admin status

	// Answer the callback query immediately
	var responseText string
	if approved {
		responseText = f.localizer.T("callback.playlist_switched", "...", "...")
	} else {
		responseText = f.localizer.T("callback.playlist_stay")
	}

	if _, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
		Text:            responseText,
	}); err != nil {
		f.logger.Debug("Failed to answer callback query", zap.Error(err))
	}

	// Remove the inline keyboard by editing the message
	if _, err := b.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{
		ChatID:    update.CallbackQuery.Message.Message.Chat.ID,
		MessageID: update.CallbackQuery.Message.Message.ID,
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{},
		},
	}); err != nil {
		f.logger.Debug("Failed to remove buttons from playlist switch message", zap.Error(err))
	}

	// Call the decision handler
	if f.playlistSwitchDecisionHandler != nil {
		f.playlistSwitchDecisionHandler(approved)
	}

	f.logger.Info("Playlist switch decision processed",
		zap.Bool("approved", approved),
		zap.String("userID", strconv.FormatInt(update.CallbackQuery.From.ID, 10)))
}

func (f *Frontend) extractAutoPlayTrackID(callbackData string, approved bool) string {
	if approved && strings.HasPrefix(callbackData, "autoplay_approve_") {
		return strings.TrimPrefix(callbackData, "autoplay_approve_")
	}
	if !approved && strings.HasPrefix(callbackData, "autoplay_deny_") {
		return strings.TrimPrefix(callbackData, "autoplay_deny_")
	}
	return ""
}

func (f *Frontend) getAdminApproval(approvalKey string) *adminApprovalContext {
	f.adminApprovalMutex.RLock()
	approval, exists := f.pendingAdminApprovals[approvalKey]
	f.adminApprovalMutex.RUnlock()

	if !exists {
		return nil
	}
	return approval
}

func (f *Frontend) isUserAdmin(userID int64, adminList []int64) bool {
	for _, adminID := range adminList {
		if userID == adminID {
			return true
		}
	}
	return false
}

// IsUserAdmin implements the chat.Frontend interface to check if a user is an admin
func (f *Frontend) IsUserAdmin(ctx context.Context, chatID, userID string) (bool, error) {
	if !f.config.Enabled {
		return false, fmt.Errorf("telegram frontend is disabled")
	}

	// Parse user ID
	userIDInt, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return false, fmt.Errorf("invalid user ID: %w", err)
	}

	// Parse chat ID
	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return false, fmt.Errorf("invalid chat ID: %w", err)
	}

	// Only check admin status for the configured group
	if chatIDInt != f.config.GroupID {
		return false, nil
	}

	// Get current admin list
	adminIDs, err := f.GetGroupAdmins(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get group admins: %w", err)
	}

	return f.isUserAdmin(userIDInt, adminIDs), nil
}

// GetAdminUserIDs implements the chat.Frontend interface to get admin user IDs as strings
func (f *Frontend) GetAdminUserIDs(ctx context.Context, chatID string) ([]string, error) {
	if !f.config.Enabled {
		return nil, fmt.Errorf("telegram frontend is disabled")
	}

	// Parse chat ID
	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid chat ID: %w", err)
	}

	// Only get admins for the configured group
	if chatIDInt != f.config.GroupID {
		return nil, fmt.Errorf("chat ID %s does not match configured group ID %d", chatID, f.config.GroupID)
	}

	// Use existing GetGroupAdmins method
	adminIDs, err := f.GetGroupAdmins(ctx)
	if err != nil {
		return nil, err // Error already includes context from GetGroupAdmins
	}

	// Convert int64 admin IDs to strings
	adminUserIDs := make([]string, len(adminIDs))
	for i, adminID := range adminIDs {
		adminUserIDs[i] = strconv.FormatInt(adminID, 10)
	}

	return adminUserIDs, nil
}

// SendDirectMessage implements the chat.Frontend interface to send direct messages to users
func (f *Frontend) SendDirectMessage(ctx context.Context, userID, text string) error {
	if !f.config.Enabled {
		return fmt.Errorf("telegram frontend is disabled")
	}

	// Parse user ID
	userIDInt, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid user ID: %w", err)
	}

	params := &bot.SendMessageParams{
		ChatID: userIDInt,
		Text:   text,
	}

	// Disable link previews for direct messages
	disabled := true
	params.LinkPreviewOptions = &models.LinkPreviewOptions{
		IsDisabled: &disabled,
	}

	_, err = f.bot.SendMessage(ctx, params)
	if err != nil {
		return fmt.Errorf("failed to send direct message to user %s: %w", userID, err)
	}

	f.logger.Debug("Sent direct message to user",
		zap.String("userID", userID),
		zap.String("text", text))

	return nil
}

func (f *Frontend) answerExpiredCallback(ctx context.Context, b *bot.Bot, callbackQueryID string) {
	if _, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: callbackQueryID,
		Text:            f.localizer.T("callback.expired"),
	}); err != nil {
		f.logger.Debug("Failed to answer callback query", zap.Error(err))
	}
}

func (f *Frontend) answerUnauthorizedCallback(ctx context.Context, b *bot.Bot, callbackQueryID string) {
	if _, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: callbackQueryID,
		Text:            f.localizer.T("callback.unauthorized"),
	}); err != nil {
		f.logger.Debug("Failed to answer callback query", zap.Error(err))
	}
}

func (f *Frontend) processAdminDecision(ctx context.Context, b *bot.Bot, update *models.Update,
	approval *adminApprovalContext, approved bool) {
	select {
	case approval.approved <- approved:
		responseText := f.buildResponseText(approved, &update.CallbackQuery.From, approval)
		f.logAdminDecision(approved, &update.CallbackQuery.From, approval)

		if _, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            responseText,
		}); err != nil {
			f.logger.Debug("Failed to answer callback query", zap.Error(err))
		}

		f.updateApprovalMessage(ctx, b, update, approval, responseText)

	case <-approval.cancelCtx.Done():
		f.answerExpiredCallback(ctx, b, update.CallbackQuery.ID)
	}
}

func (f *Frontend) buildResponseText(approved bool, admin *models.User, _ *adminApprovalContext) string {
	adminName := f.getUserDisplayName(admin)
	if approved {
		return "✅ Approved by " + adminName
	}
	return "❌ Denied by " + adminName
}

func (f *Frontend) logAdminDecision(approved bool, admin *models.User, approval *adminApprovalContext) {
	adminName := f.getUserDisplayName(admin)
	if approved {
		f.logger.Info("Admin approved song request",
			zap.String("admin", adminName),
			zap.String("user", approval.originUserName),
			zap.String("song", approval.songInfo))
	} else {
		f.logger.Info("Admin denied song request",
			zap.String("admin", adminName),
			zap.String("user", approval.originUserName),
			zap.String("song", approval.songInfo))
	}
}

func (f *Frontend) updateApprovalMessage(ctx context.Context, b *bot.Bot, update *models.Update,
	approval *adminApprovalContext, responseText string) {
	if update.CallbackQuery.Message.Message != nil {
		text := fmt.Sprintf("🎵 Admin Approval: %s\n\nUser: %s\nSong: %s\n\n%s",
			responseText, approval.originUserName, approval.songInfo, responseText)

		if _, err := b.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    update.CallbackQuery.From.ID,
			MessageID: update.CallbackQuery.Message.Message.ID,
			Text:      text,
		}); err != nil {
			f.logger.Debug("Failed to edit admin approval message", zap.Error(err))
		}
	}
}

// GroupInfo represents a Telegram group/chat information
type GroupInfo struct {
	ID    int64
	Title string
	Type  string
}

// ListAvailableGroups returns a list of groups the bot is part of
func (f *Frontend) ListAvailableGroups(ctx context.Context) ([]GroupInfo, error) {
	var groups []GroupInfo
	groupsMap := make(map[int64]GroupInfo)

	// Create a separate bot instance just for group discovery without default handler
	tempHandler := func(_ context.Context, _ *bot.Bot, update *models.Update) {
		f.logger.Info("Received update during group discovery")

		var chat models.Chat
		var hasChat bool

		if update.Message != nil {
			chat = update.Message.Chat
			hasChat = true
			f.logger.Info("Found message in chat",
				zap.Int64("chatID", chat.ID),
				zap.String("chatTitle", chat.Title),
				zap.String("chatType", string(chat.Type)))
		} else if update.CallbackQuery != nil && update.CallbackQuery.Message.Message != nil {
			chat = update.CallbackQuery.Message.Message.Chat
			hasChat = true
			f.logger.Info("Found callback query in chat",
				zap.Int64("chatID", chat.ID),
				zap.String("chatTitle", chat.Title),
				zap.String("chatType", string(chat.Type)))
		}

		if hasChat && (string(chat.Type) == chatTypeGroup || string(chat.Type) == chatTypeSuperGroup) {
			f.logger.Info("Discovered group during scan",
				zap.Int64("groupID", chat.ID),
				zap.String("groupTitle", chat.Title),
				zap.String("groupType", string(chat.Type)))
			groupsMap[chat.ID] = GroupInfo{
				ID:    chat.ID,
				Title: chat.Title,
				Type:  string(chat.Type),
			}
		} else if hasChat {
			f.logger.Info("Ignoring non-group chat",
				zap.Int64("chatID", chat.ID),
				zap.String("chatType", string(chat.Type)))
		}
	}

	// Create a temporary bot with only our handler (no default handler)
	// Note: We can't easily suppress the "context canceled" error from the bot library
	// as it uses the standard log package internally. The error is expected and harmless.
	tempBot, err := bot.New(f.config.BotToken,
		bot.WithDefaultHandler(tempHandler))
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary bot for group discovery: %w", err)
	}

	f.logger.Info("Created temporary bot for group discovery")

	// Start the bot to receive updates
	discoverCtx, cancelDiscover := context.WithTimeout(ctx, groupDiscoveryTimeout*time.Second)
	defer cancelDiscover()

	// Start bot polling in background
	go func() {
		// Suppress the expected "context canceled" error from bot polling
		defer func() {
			if r := recover(); r != nil {
				// Log unexpected panics but ignore context cancellation
				f.logger.Debug("Bot polling stopped", zap.Any("reason", r))
			}
		}()
		tempBot.Start(discoverCtx)
	}()

	// Give some time to collect updates
	f.logger.Info("Scanning for groups... Please send a message in any group the bot should monitor")
	f.logger.Info("Waiting 15 seconds for group discovery...")

	// Wait for groups to be discovered
	select {
	case <-time.After(groupDiscoveryTimeout * time.Second):
		// Timeout - proceed with discovered groups

		// Temporarily suppress stderr to hide the expected "context canceled" error
		originalOutput := log.Writer()
		log.SetOutput(io.Discard)

		cancelDiscover() // Stop the bot polling

		// Give a brief moment for the bot to stop and any error messages to be discarded
		time.Sleep(botStopDelay)

		// Restore stderr
		log.SetOutput(originalOutput)

	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Convert map to slice
	for _, group := range groupsMap {
		groups = append(groups, group)
	}

	f.logger.Info("Group discovery completed",
		zap.Int("groupCount", len(groups)),
		zap.Any("groups", groups))

	// Add a small delay to let any remaining bot error messages print before our output
	time.Sleep(discoveryFinalWait)

	return groups, nil
}

// AwaitCommunityApproval waits for enough community 👍 reactions to bypass admin approval
func (f *Frontend) AwaitCommunityApproval(ctx context.Context, msgID string, requiredReactions, timeoutSec int) (bool, error) {
	if !f.config.Enabled {
		return false, fmt.Errorf("telegram frontend is disabled")
	}

	// If community approval is disabled (0), return false immediately
	if f.config.CommunityApproval <= 0 || requiredReactions <= 0 {
		return false, nil
	}

	messageID, err := strconv.Atoi(msgID)
	if err != nil {
		return false, fmt.Errorf("invalid message ID: %w", err)
	}

	// Create community approval context
	approvalCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	communityApproval := &communityApprovalContext{
		messageID:         messageID,
		requiredReactions: requiredReactions,
		currentReactions:  0,
		reactedUsers:      make(map[int64]bool),
		approved:          make(chan bool, 1),
		cancelCtx:         approvalCtx,
		cancelFunc:        cancel,
	}

	// Generate unique key for this community approval
	approvalKey := fmt.Sprintf("community_%s_%d", msgID, time.Now().Unix())

	f.communityApprovalMutex.Lock()
	f.pendingCommunityApprovals[approvalKey] = communityApproval
	f.communityApprovalMutex.Unlock()

	// Cleanup function
	defer func() {
		cancel()
		f.communityApprovalMutex.Lock()
		delete(f.pendingCommunityApprovals, approvalKey)
		f.communityApprovalMutex.Unlock()
	}()

	f.logger.Debug("Started community approval tracking",
		zap.String("message_id", msgID),
		zap.Int("required_reactions", requiredReactions),
		zap.Int("timeout_sec", timeoutSec))

	// Wait for approval or timeout
	select {
	case approved := <-communityApproval.approved:
		f.logger.Info("Community approval completed",
			zap.String("message_id", msgID),
			zap.Bool("approved", approved),
			zap.Int("final_reactions", communityApproval.currentReactions))
		return approved, nil
	case <-approvalCtx.Done():
		f.logger.Debug("Community approval timed out",
			zap.String("message_id", msgID),
			zap.Int("final_reactions", communityApproval.currentReactions),
			zap.Int("required_reactions", requiredReactions))
		return false, nil
	}
}

// SendAutoPlayApproval sends an auto-play prevention message with approve/deny buttons
func (f *Frontend) SendAutoPlayApproval(ctx context.Context, chatID, trackID, message string) (string, error) {
	if !f.config.Enabled {
		return "", nil
	}

	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid chat ID: %w", err)
	}

	keyboard := [][]models.InlineKeyboardButton{
		{
			{
				Text:         f.localizer.T("button.autoplay_approve"),
				CallbackData: "autoplay_approve_" + trackID,
			},
			{
				Text:         f.localizer.T("button.autoplay_deny"),
				CallbackData: "autoplay_deny_" + trackID,
			},
		},
	}

	// Disable link previews for auto-play messages since they contain Spotify links
	disabled := true
	sentMsg, err := f.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:             chatIDInt,
		Text:               message,
		ReplyMarkup:        models.InlineKeyboardMarkup{InlineKeyboard: keyboard},
		LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: &disabled},
	})
	if err != nil {
		return "", fmt.Errorf("failed to send auto-play approval message: %w", err)
	}

	f.logger.Debug("Sent auto-play approval message",
		zap.String("chatID", chatID),
		zap.String("trackID", trackID),
		zap.Int("messageID", sentMsg.ID))

	return strconv.Itoa(sentMsg.ID), nil
}

// SetAutoPlayDecisionHandler sets the handler for auto-play approval/denial decisions
func (f *Frontend) SetAutoPlayDecisionHandler(handler func(trackID string, approved bool)) {
	f.autoPlayDecisionHandler = handler
}

// SendPlaylistSwitchApproval sends a playlist switch warning with switch/stay buttons
func (f *Frontend) SendPlaylistSwitchApproval(ctx context.Context, chatID, message string) (string, error) {
	groupID, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid chat ID: %w", err)
	}

	keyboard := &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{
				{
					Text:         f.localizer.T("button.switch_playlist"),
					CallbackData: "playlist_switch_approve",
				},
				{
					Text:         f.localizer.T("button.stay_current"),
					CallbackData: "playlist_switch_deny",
				},
			},
		},
	}

	resp, err := f.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      groupID,
		Text:        message,
		ReplyMarkup: keyboard,
	})

	if err != nil {
		return "", fmt.Errorf("failed to send playlist switch approval: %w", err)
	}

	return strconv.Itoa(resp.ID), nil
}

// SetPlaylistSwitchDecisionHandler sets the handler for playlist switch decisions
func (f *Frontend) SetPlaylistSwitchDecisionHandler(handler func(approved bool)) {
	f.playlistSwitchDecisionHandler = handler
}

// EditMessage edits an existing message by ID
func (f *Frontend) EditMessage(ctx context.Context, chatID, messageID, newText string) error {
	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat ID: %w", err)
	}

	messageIDInt, err := strconv.Atoi(messageID)
	if err != nil {
		return fmt.Errorf("invalid message ID: %w", err)
	}

	if newText == "" {
		// Only remove inline keyboard without changing text
		_, err = f.bot.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{
			ChatID:    chatIDInt,
			MessageID: messageIDInt,
			// No ReplyMarkup means removing the inline keyboard
		})
	} else {
		// Edit the message text and remove inline keyboard
		_, err = f.bot.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatIDInt,
			MessageID: messageIDInt,
			Text:      newText,
			// No ReplyMarkup means removing the inline keyboard
		})
	}

	if err != nil {
		return fmt.Errorf("failed to edit message: %w", err)
	}

	f.logger.Debug("Edited message",
		zap.String("chatID", chatID),
		zap.String("messageID", messageID),
		zap.String("newText", newText),
		zap.Bool("onlyRemovedButtons", newText == ""))

	return nil
}
