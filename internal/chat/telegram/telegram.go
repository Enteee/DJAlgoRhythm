// Package telegram provides Telegram Bot API integration using go-telegram/bot library.
package telegram

import (
	"context"
	"errors"
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

	"djalgorhythm/internal/chat"
	"djalgorhythm/internal/flood"
	"djalgorhythm/internal/i18n"
	"djalgorhythm/pkg/text"
)

const (
	entityTypeURL         = "url"
	chatTypeGroup         = "group"
	chatTypeSuperGroup    = "supergroup"
	groupDiscoveryTimeout = 15 // seconds for group discovery
	thumbsUpEmoji         = "👍"
	floodEmoji            = "🌊"
	// Sleep durations for group discovery.
	botStopDelay       = 200 * time.Millisecond
	discoveryFinalWait = 50 * time.Millisecond
	cleanupTimeout     = 5 * time.Second // Timeout for cleanup operations
)

// Config holds Telegram-specific configuration.
type Config struct {
	BotToken            string
	GroupID             int64 // Chat ID of the group to monitor
	Enabled             bool
	AdminApproval       bool   // Whether admin approval is required for songs
	AdminNeedsApproval  bool   // Whether admins also need approval (for testing)
	CommunityApproval   int    // Number of 👍 reactions needed to bypass admin approval (0 disables)
	Language            string // Bot language for user-facing messages
	FloodLimitPerMinute int    // Maximum messages per user per minute
}

// Frontend implements the chat.Frontend interface for Telegram.
type Frontend struct {
	config         *Config
	logger         *zap.Logger
	bot            *bot.Bot
	parser         *text.Parser
	localizer      *i18n.Localizer
	floodgate      *flood.Floodgate
	coreGroupIDPtr *int64 // Pointer to core config's GroupID for migration sync

	// Message handling
	messageHandler func(*chat.Message)

	// Queue track decision handling
	queueTrackDecisionHandler func(ctx context.Context, trackID string, approved bool)

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

// approvalContext tracks pending user approvals.
type approvalContext struct {
	originUserID int64
	approved     chan bool
	cancelCtx    context.Context //nolint:containedctx // Required for timeout cancellation management
	cancelFunc   context.CancelFunc
}

// adminApprovalContext tracks pending admin approvals.
type adminApprovalContext struct {
	originUserID   int64
	originUserName string
	songInfo       string
	songURL        string
	trackMood      string
	approved       chan bool
	cancelCtx      context.Context //nolint:containedctx // Required for timeout cancellation management
	cancelFunc     context.CancelFunc
	sentMessages   map[int64]int // admin ID -> message ID mapping for cleanup
}

// communityApprovalContext tracks pending community approvals via reactions.
type communityApprovalContext struct {
	messageID         int
	requiredReactions int
	currentReactions  int
	reactedUsers      map[int64]bool // track users who reacted to prevent double counting
	requesterUserID   int64          // original song requester user ID (to prevent self-approval)
	approved          chan bool
	cancelCtx         context.Context //nolint:containedctx // Required for timeout cancellation management
	cancelFunc        context.CancelFunc
}

// NewFrontend creates a new Telegram frontend.
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
		floodgate:                 flood.New(config.FloodLimitPerMinute),
		pendingApprovals:          make(map[string]*approvalContext),
		pendingAdminApprovals:     make(map[string]*adminApprovalContext),
		pendingCommunityApprovals: make(map[string]*communityApprovalContext),
	}
}

// SetCoreGroupIDPointer sets a pointer to the core config's GroupID field.
// This enables automatic synchronization when chat migration occurs (group -> supergroup).
func (f *Frontend) SetCoreGroupIDPointer(groupIDPtr *int64) {
	f.coreGroupIDPtr = groupIDPtr
}

// Start initializes the Telegram bot and begins listening for updates.
func (f *Frontend) Start(ctx context.Context) error {
	if !f.config.Enabled {
		f.logger.Info("Telegram frontend is disabled, skipping initialization")
		return nil
	}

	f.logger.Info("Starting Telegram frontend",
		zap.String("group_id", strconv.FormatInt(f.config.GroupID, 10)))

	opts := []bot.Option{
		bot.WithDefaultHandler(f.handleUpdate),
		bot.WithCallbackQueryDataHandler("confirm_", bot.MatchTypePrefix, f.handleConfirmCallback),
		bot.WithCallbackQueryDataHandler("reject_", bot.MatchTypePrefix, f.handleRejectCallback),
		bot.WithCallbackQueryDataHandler("admin_approve_", bot.MatchTypePrefix,
			func(ctx context.Context, b *bot.Bot, update *models.Update) {
				f.handleAdminApprovalCallback(ctx, b, update, true)
			}),

		bot.WithCallbackQueryDataHandler("admin_deny_", bot.MatchTypePrefix,
			func(ctx context.Context, b *bot.Bot, update *models.Update) {
				f.handleAdminApprovalCallback(ctx, b, update, false)
			}),

		bot.WithCallbackQueryDataHandler("queue_approve_", bot.MatchTypePrefix,
			func(ctx context.Context, b *bot.Bot, update *models.Update) {
				f.handleQueueTrackCallback(ctx, b, update, true)
			}),

		bot.WithCallbackQueryDataHandler("queue_deny_", bot.MatchTypePrefix,
			func(ctx context.Context, b *bot.Bot, update *models.Update) {
				f.handleQueueTrackCallback(ctx, b, update, false)
			}),
		// Configure allowed updates to include reaction events for community approval
		bot.WithAllowedUpdates([]string{
			"message",
			"callback_query",
			"message_reaction",
			"message_reaction_count",
		}),
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

// Listen starts listening for messages and calls the handler for each message.
func (f *Frontend) Listen(ctx context.Context, handler func(*chat.Message)) error {
	if !f.config.Enabled {
		return nil // Do nothing if disabled
	}

	f.messageHandler = handler

	// Start the bot
	f.bot.Start(ctx)

	return nil
}

// SendText sends a text message to the specified chat, optionally as a reply.
func (f *Frontend) SendText(ctx context.Context, chatID, replyToID, message string) (string, error) {
	if !f.config.Enabled {
		return "", errors.New("telegram frontend is disabled")
	}

	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid chat ID: %w", err)
	}

	params := &bot.SendMessageParams{
		ChatID: chatIDInt,
		Text:   message,
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

	msg, err := f.sendMessageWithMigrationHandling(ctx, params)
	if err != nil {
		return "", fmt.Errorf("failed to send message: %w", err)
	}

	return strconv.Itoa(msg.ID), nil
}

// DeleteMessage deletes a message by its ID.
func (f *Frontend) DeleteMessage(ctx context.Context, chatID, msgID string) error {
	if !f.config.Enabled {
		return errors.New("telegram frontend is disabled")
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

// React adds an emoji reaction to a message.
func (f *Frontend) React(ctx context.Context, chatID, msgID string, r chat.Reaction) error {
	if !f.config.Enabled {
		return errors.New("telegram frontend is disabled")
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

// AwaitApproval waits for user approval via reaction or inline buttons.
func (f *Frontend) AwaitApproval(ctx context.Context, origin *chat.Message, prompt string,
	timeoutSec int) (bool, error) {
	if !f.config.Enabled {
		return false, errors.New("telegram frontend is disabled")
	}

	chatIDInt, originalUserID, err := f.parseMessageIDs(origin)
	if err != nil {
		return false, err
	}

	approval, approvalKey := f.createApprovalContext(ctx, origin, originalUserID, timeoutSec)

	defer f.cleanupApproval(approvalKey, approval.cancelFunc)

	promptMsgID, err := f.sendApprovalPrompt(ctx, origin, prompt, approvalKey, chatIDInt)
	if err != nil {
		return false, err
	}

	response := f.awaitApprovalResponse(ctx, approval, chatIDInt, promptMsgID)
	return response, nil
}

// parseMessageIDs extracts and validates chat ID and user ID from the origin message.
func (f *Frontend) parseMessageIDs(origin *chat.Message) (chatID, replyID int64, err error) {
	chatIDInt, err := strconv.ParseInt(origin.ChatID, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid chat ID: %w", err)
	}

	originalUserID, err := strconv.ParseInt(origin.SenderID, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid sender ID: %w", err)
	}

	return chatIDInt, originalUserID, nil
}

// createApprovalContext creates and registers a new approval context.
func (f *Frontend) createApprovalContext(ctx context.Context, origin *chat.Message,
	originalUserID int64, timeoutSec int) (approval *approvalContext, messageID string) {
	approvalCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	approval = &approvalContext{
		originUserID: originalUserID,
		approved:     make(chan bool, 1),
		cancelCtx:    approvalCtx,
		cancelFunc:   cancel,
	}

	approvalKey := fmt.Sprintf("%s_%s_%d", origin.ChatID, origin.ID, time.Now().Unix())

	f.approvalMutex.Lock()
	f.pendingApprovals[approvalKey] = approval
	f.approvalMutex.Unlock()

	messageID = approvalKey
	return
}

// cleanupApproval removes the approval context and cancels its context.
func (f *Frontend) cleanupApproval(approvalKey string, cancel context.CancelFunc) {
	cancel()
	f.approvalMutex.Lock()
	delete(f.pendingApprovals, approvalKey)
	f.approvalMutex.Unlock()
}

// sendApprovalPrompt sends the approval prompt message with inline keyboard.
func (f *Frontend) sendApprovalPrompt(ctx context.Context, origin *chat.Message,
	prompt, approvalKey string, chatIDInt int64) (int, error) {
	originalMsgID, _ := strconv.Atoi(origin.ID)

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

	disabled := true
	params := &bot.SendMessageParams{
		ChatID:      chatIDInt,
		Text:        prompt,
		ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: keyboard},
		ReplyParameters: &models.ReplyParameters{
			MessageID: originalMsgID,
		},
		LinkPreviewOptions: &models.LinkPreviewOptions{
			IsDisabled: &disabled,
		},
	}

	promptMsg, err := f.sendMessageWithMigrationHandling(ctx, params)
	if err != nil {
		return 0, fmt.Errorf("failed to send approval prompt: %w", err)
	}

	return promptMsg.ID, nil
}

// awaitApprovalResponse waits for the user's approval response or timeout.
func (f *Frontend) awaitApprovalResponse(ctx context.Context, approval *approvalContext,
	chatIDInt int64, promptMsgID int) bool {
	select {
	case approved := <-approval.approved:
		f.cleanupPromptMessage(ctx, chatIDInt, promptMsgID)
		return approved
	case <-approval.cancelCtx.Done():
		f.cleanupPromptMessage(ctx, chatIDInt, promptMsgID)
		return false
	}
}

// cleanupPromptMessage deletes the prompt message and logs any errors.
func (f *Frontend) cleanupPromptMessage(ctx context.Context, chatIDInt int64, promptMsgID int) {
	if delErr := f.DeleteMessage(ctx, strconv.FormatInt(chatIDInt, 10), strconv.Itoa(promptMsgID)); delErr != nil {
		f.logger.Debug("Failed to delete prompt message", zap.Error(delErr))
	}
}

// handleUpdate processes incoming Telegram updates.
func (f *Frontend) handleUpdate(ctx context.Context, _ *bot.Bot, update *models.Update) {
	// Debug logging to track all incoming update types
	f.logger.Debug("Received Telegram update",
		zap.Bool("has_message", update.Message != nil),
		zap.Bool("has_callback_query", update.CallbackQuery != nil),
		zap.Bool("has_message_reaction", update.MessageReaction != nil),
		zap.Bool("has_message_reaction_count", update.MessageReactionCount != nil))

	if update.Message != nil {
		f.handleMessage(ctx, update.Message)
	}

	// Handle individual message reactions (for more granular tracking)
	if update.MessageReaction != nil {
		f.handleMessageReaction(ctx, update.MessageReaction)
	}

	// Handle message reaction count updates (aggregate counts)
	if update.MessageReactionCount != nil {
		f.handleMessageReactionCount(ctx, update.MessageReactionCount)
	}
}

// handleMessage processes incoming messages.
func (f *Frontend) handleMessage(ctx context.Context, msg *models.Message) {
	// Only process messages from the configured group
	if msg.Chat.ID != f.config.GroupID {
		return
	}

	// Ignore messages from the bot itself
	if msg.From.IsBot {
		return
	}

	// Check if this is a service message and ignore it
	if f.isServiceMessage(msg) {
		f.logger.Debug("Ignoring service message",
			zap.String("type", f.getServiceMessageType(msg)),
			zap.String("text", msg.Text))
		return
	}

	// Check flood prevention - block messages that exceed rate limit
	chatID := strconv.FormatInt(msg.Chat.ID, 10)
	userID := strconv.FormatInt(msg.From.ID, 10)
	if !f.floodgate.CheckMessage(chatID, userID) {
		f.logger.Debug("Message blocked due to flood prevention",
			zap.String("chatID", chatID),
			zap.String("userID", userID),
			zap.String("userName", f.getUserDisplayName(msg.From)))

		// React with flood emoji to indicate the message was blocked
		if err := f.React(ctx, chatID, strconv.Itoa(msg.ID), chat.ReactionYawning); err != nil {
			f.logger.Debug("Failed to add flood reaction to message", zap.Error(err))
		}
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

// handleMessageReactionCount processes incoming message reaction count updates for community approval.
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

// processReactionCountForCommunityApproval processes a reaction count update for community approval.
func (f *Frontend) processReactionCountForCommunityApproval(
	approval *communityApprovalContext, reactionCount *models.MessageReactionCountUpdated) {
	// Count 👍 reactions
	thumbsUpCount := 0

	for _, reaction := range reactionCount.Reactions {
		if reaction.Type.Type == models.ReactionTypeTypeEmoji &&
			reaction.Type.ReactionTypeEmoji != nil &&
			reaction.Type.ReactionTypeEmoji.Emoji == thumbsUpEmoji {
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

// handleMessageReaction processes individual message reaction updates for community approval.
func (f *Frontend) handleMessageReaction(_ context.Context, reaction *models.MessageReactionUpdated) {
	// Only process reactions from the configured group
	if reaction.Chat.ID != f.config.GroupID {
		return
	}

	// Get the actor ID (user or chat)
	var actorID int64
	var actorType string
	switch {
	case reaction.User != nil:
		actorID = reaction.User.ID
		actorType = "user"
	case reaction.ActorChat != nil:
		actorID = reaction.ActorChat.ID
		actorType = "chat"
	default:
		f.logger.Warn("Received reaction with no actor information")
		return
	}

	f.logger.Debug("Received individual message reaction",
		zap.Int("message_id", reaction.MessageID),
		zap.Int64("actor_id", actorID),
		zap.String("actor_type", actorType),
		zap.Int("reaction_count", len(reaction.NewReaction)))

	// Check if there are any pending community approvals for this message
	f.communityApprovalMutex.Lock()
	defer f.communityApprovalMutex.Unlock()

	for _, approval := range f.pendingCommunityApprovals {
		if approval.messageID == reaction.MessageID {
			f.processIndividualReactionForCommunityApproval(approval, reaction)
			break
		}
	}
}

// processIndividualReactionForCommunityApproval processes an individual reaction for community approval.
func (f *Frontend) processIndividualReactionForCommunityApproval(
	approval *communityApprovalContext, reaction *models.MessageReactionUpdated,
) {
	// Get the actor ID (user or chat)
	userID, ok := getReactionActorID(reaction)
	if !ok {
		f.logger.Warn("Cannot process reaction: no actor information available")
		return
	}

	// Prevent self-approval: ignore reactions from the original song requester
	if userID == approval.requesterUserID {
		f.logger.Debug("Ignoring reaction from original song requester (self-approval prevention)",
			zap.Int("message_id", approval.messageID),
			zap.Int64("requester_user_id", approval.requesterUserID),
			zap.Int64("actor_user_id", userID))
		return
	}

	// Check if user added or removed a 👍 reaction
	hasThumbsUp := hasThumbsUpReaction(reaction.NewReaction)

	// Update user tracking
	previouslyReacted := approval.reactedUsers[userID]
	if hasThumbsUp && !previouslyReacted {
		// User added thumbs up
		approval.reactedUsers[userID] = true
		approval.currentReactions++
		f.logger.Debug("User added thumbs up reaction",
			zap.Int("message_id", approval.messageID),
			zap.Int64("user_id", userID),
			zap.Int("current_reactions", approval.currentReactions),
			zap.Int("required_reactions", approval.requiredReactions))
	} else if !hasThumbsUp && previouslyReacted {
		// User removed thumbs up
		delete(approval.reactedUsers, userID)
		approval.currentReactions--
		f.logger.Debug("User removed thumbs up reaction",
			zap.Int("message_id", approval.messageID),
			zap.Int64("user_id", userID),
			zap.Int("current_reactions", approval.currentReactions),
			zap.Int("required_reactions", approval.requiredReactions))
	}

	// Check if we've reached the required number of reactions
	if approval.currentReactions >= approval.requiredReactions {
		select {
		case approval.approved <- true:
			f.logger.Info("Community approval achieved via individual reactions",
				zap.Int("message_id", approval.messageID),
				zap.Int("reactions_received", approval.currentReactions),
				zap.Int("reactions_required", approval.requiredReactions))
		case <-approval.cancelCtx.Done():
			// Context already canceled, do nothing
		}
	}
}

// getReactionActorID extracts the user ID from a reaction update.
func getReactionActorID(reaction *models.MessageReactionUpdated) (int64, bool) {
	switch {
	case reaction.User != nil:
		return reaction.User.ID, true
	case reaction.ActorChat != nil:
		return reaction.ActorChat.ID, true
	default:
		return 0, false
	}
}

// hasThumbsUpReaction checks if a thumbs up emoji is present in reactions.
func hasThumbsUpReaction(reactions []models.ReactionType) bool {
	for _, reactionType := range reactions {
		if reactionType.Type == models.ReactionTypeTypeEmoji &&
			reactionType.ReactionTypeEmoji != nil &&
			reactionType.ReactionTypeEmoji.Emoji == thumbsUpEmoji {
			return true
		}
	}
	return false
}

// handleConfirmCallback handles confirmation button clicks.
func (f *Frontend) handleConfirmCallback(ctx context.Context, b *bot.Bot, update *models.Update) {
	f.handleApprovalCallback(ctx, b, update, true)
}

// handleRejectCallback handles rejection button clicks.
func (f *Frontend) handleRejectCallback(ctx context.Context, b *bot.Bot, update *models.Update) {
	f.handleApprovalCallback(ctx, b, update, false)
}

// handleApprovalCallback handles both confirm and reject callbacks.
func (f *Frontend) handleApprovalCallback(ctx context.Context, b *bot.Bot, update *models.Update, approved bool) {
	if update.CallbackQuery == nil {
		return
	}

	approvalKey := f.extractApprovalKey(update.CallbackQuery.Data, approved)
	if approvalKey == "" {
		return
	}

	approval := f.getApproval(approvalKey)
	if approval == nil {
		f.answerExpiredCallback(ctx, b, update.CallbackQuery.ID)
		return
	}

	if !f.validateApprovalUser(ctx, b, update.CallbackQuery, approval) {
		return
	}

	f.sendApprovalResult(ctx, b, update.CallbackQuery, approval, approved)
}

// extractApprovalKey extracts the approval key from callback data.
func (f *Frontend) extractApprovalKey(callbackData string, approved bool) string {
	switch {
	case approved && strings.HasPrefix(callbackData, "confirm_"):
		return strings.TrimPrefix(callbackData, "confirm_")
	case !approved && strings.HasPrefix(callbackData, "reject_"):
		return strings.TrimPrefix(callbackData, "reject_")
	default:
		return ""
	}
}

// getApproval safely retrieves an approval context.
func (f *Frontend) getApproval(approvalKey string) *approvalContext {
	f.approvalMutex.RLock()
	defer f.approvalMutex.RUnlock()
	approval, exists := f.pendingApprovals[approvalKey]
	if !exists {
		return nil
	}
	return approval
}

// answerExpiredCallback responds to an expired callback query.
func (f *Frontend) answerExpiredCallback(ctx context.Context, b *bot.Bot, callbackQueryID string) {
	if _, ansErr := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: callbackQueryID,
		Text:            f.localizer.T("callback.prompt_expired"),
	}); ansErr != nil {
		f.logger.Debug("Failed to answer callback query", zap.Error(ansErr))
	}
}

// validateApprovalUser checks if the callback is from the authorized user.
func (f *Frontend) validateApprovalUser(ctx context.Context, b *bot.Bot,
	callbackQuery *models.CallbackQuery, approval *approvalContext) bool {
	if callbackQuery.From.ID != approval.originUserID {
		if _, ansErr := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: callbackQuery.ID,
			Text:            f.localizer.T("callback.sender_only"),
		}); ansErr != nil {
			f.logger.Debug("Failed to answer callback query", zap.Error(ansErr))
		}
		return false
	}
	return true
}

// sendApprovalResult sends the approval decision and responds to the callback.
func (f *Frontend) sendApprovalResult(ctx context.Context, b *bot.Bot,
	callbackQuery *models.CallbackQuery, approval *approvalContext, approved bool) {
	select {
	case approval.approved <- approved:
		responseText := f.localizer.T("callback.denied")
		if approved {
			responseText = f.localizer.T("callback.approved")
		}

		if _, ansErr := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: callbackQuery.ID,
			Text:            responseText,
		}); ansErr != nil {
			f.logger.Debug("Failed to answer callback query", zap.Error(ansErr))
		}
	case <-approval.cancelCtx.Done():
		f.answerExpiredCallback(ctx, b, callbackQuery.ID)
	}
}

// verifyGroupAccess checks if the bot has access to the configured group.
func (f *Frontend) verifyGroupAccess(ctx context.Context) error {
	chatInfo, err := f.bot.GetChat(ctx, &bot.GetChatParams{
		ChatID: f.config.GroupID,
	})
	if err != nil {
		return fmt.Errorf("cannot access group %d: %w", f.config.GroupID, err)
	}

	f.logger.Info("Bot has access to group",
		zap.String("group_title", chatInfo.Title),
		zap.String("group_type", string(chatInfo.Type)))

	return nil
}

// extractURLs extracts URLs from message entities.
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

// getUserDisplayName creates a display name for the user.
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

// IsAdminApprovalEnabled returns whether admin approval is enabled.
func (f *Frontend) IsAdminApprovalEnabled() bool {
	return f.config.AdminApproval && f.config.Enabled
}

// GetGroupAdmins returns a list of admin user IDs for the configured group.
func (f *Frontend) GetGroupAdmins(ctx context.Context) ([]int64, error) {
	if !f.config.Enabled {
		return nil, errors.New("telegram frontend is disabled")
	}

	admins, err := f.bot.GetChatAdministrators(ctx, &bot.GetChatAdministratorsParams{
		ChatID: f.config.GroupID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get chat administrators: %w", err)
	}

	adminIDs := extractNonBotAdminIDs(admins)

	f.logger.Debug("Retrieved group admins",
		zap.Int("count", len(adminIDs)),
		zap.Int64s("admin_ids", adminIDs))

	return adminIDs, nil
}

// extractNonBotAdminIDs extracts non-bot user IDs from chat members.
func extractNonBotAdminIDs(admins []models.ChatMember) []int64 {
	var adminIDs []int64
	for _, admin := range admins {
		user := extractAdminUser(&admin)
		// Skip bots from admin list
		if user != nil && !user.IsBot {
			adminIDs = append(adminIDs, user.ID)
		}
	}
	return adminIDs
}

// extractAdminUser extracts user from a chat member based on type.
func extractAdminUser(admin *models.ChatMember) *models.User {
	switch admin.Type {
	case models.ChatMemberTypeOwner:
		if admin.Owner != nil && admin.Owner.User != nil {
			return admin.Owner.User
		}
	case models.ChatMemberTypeAdministrator:
		if admin.Administrator != nil {
			return &admin.Administrator.User
		}
	case models.ChatMemberTypeMember, models.ChatMemberTypeRestricted,
		models.ChatMemberTypeLeft, models.ChatMemberTypeBanned:
		// Non-admin types return nil
		return nil
	}
	return nil
}

// AwaitAdminApproval requests approval from group administrators.
func (f *Frontend) AwaitAdminApproval(
	ctx context.Context, origin *chat.Message, songInfo, songURL, trackMood string, timeoutSec int) (bool, error) {
	if !f.config.Enabled {
		return false, errors.New("telegram frontend is disabled")
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
		trackMood:      trackMood,
		approved:       make(chan bool, 1),
		cancelCtx:      approvalCtx,
		cancelFunc:     cancel,
		sentMessages:   make(map[int64]int),
	}

	// Generate unique key for this admin approval
	approvalKey := fmt.Sprintf("admin_%s_%s_%d", origin.ChatID, origin.ID, time.Now().Unix())

	f.adminApprovalMutex.Lock()
	f.pendingAdminApprovals[approvalKey] = adminApproval
	f.adminApprovalMutex.Unlock()

	// Cleanup function
	defer func() {
		cancel()

		// Delete admin approval messages when context is canceled or approval completes
		// Use a fresh context with timeout for cleanup, as the original context may be canceled
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cleanupCancel()
		f.deleteAdminApprovalMessages(cleanupCtx, adminApproval)

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

// sendAdminApprovalRequests sends DM approval requests to all group admins.
func (f *Frontend) sendAdminApprovalRequests(ctx context.Context, adminIDs []int64,
	approvalKey string, approval *adminApprovalContext) error {
	prompt, keyboard := f.buildAdminApprovalMessage(approval, approvalKey)

	successCount, errs := f.sendToAdmins(ctx, adminIDs, prompt, keyboard, approval, approvalKey)

	if successCount == 0 {
		return errors.New("failed to send approval request to any admin")
	}

	f.logAdminApprovalResults(adminIDs, successCount, errs)
	return nil
}

// buildAdminApprovalMessage creates the prompt text and keyboard for admin approval.
func (f *Frontend) buildAdminApprovalMessage(approval *adminApprovalContext,
	approvalKey string) (string, [][]models.InlineKeyboardButton) {
	prompt := f.localizer.T("admin.approval_prompt",
		approval.originUserName,
		approval.songInfo,
		approval.songURL,
		approval.trackMood)

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

	return prompt, keyboard
}

// sendToAdmins sends the approval message to each admin and tracks results.
func (f *Frontend) sendToAdmins(ctx context.Context, adminIDs []int64, prompt string,
	keyboard [][]models.InlineKeyboardButton, approval *adminApprovalContext,
	approvalKey string) (int, []error) {
	var errs []error
	successCount := 0

	for _, adminID := range adminIDs {
		if f.sendToSingleAdmin(ctx, adminID, prompt, keyboard, approval, approvalKey) {
			successCount++
		} else {
			errs = append(errs, fmt.Errorf("failed to send to admin %d", adminID))
		}
	}

	return successCount, errs
}

// sendToSingleAdmin sends the approval message to a single admin.
func (f *Frontend) sendToSingleAdmin(ctx context.Context, adminID int64, prompt string,
	keyboard [][]models.InlineKeyboardButton, approval *adminApprovalContext,
	approvalKey string) bool {
	disabled := true
	params := &bot.SendMessageParams{
		ChatID:      adminID,
		Text:        prompt,
		ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: keyboard},
		LinkPreviewOptions: &models.LinkPreviewOptions{
			IsDisabled: &disabled,
		},
	}

	msg, err := f.sendMessageWithMigrationHandling(ctx, params)
	if err != nil {
		f.logger.Warn("Failed to send admin approval request",
			zap.Int64("admin_id", adminID),
			zap.Error(err))
		return false
	}

	approval.sentMessages[adminID] = msg.ID
	f.logger.Debug("Sent admin approval request",
		zap.Int64("admin_id", adminID),
		zap.String("approval_key", approvalKey),
		zap.Int("message_id", msg.ID))
	return true
}

// logAdminApprovalResults logs the results of sending admin approval requests.
func (f *Frontend) logAdminApprovalResults(adminIDs []int64, successCount int, errs []error) {
	f.logger.Info("Sent admin approval requests",
		zap.Int("total_admins", len(adminIDs)),
		zap.Int("successful", successCount),
		zap.Int("failed", len(errs)))
}

// handleAdminApprovalCallback handles both admin approve and deny callbacks.
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

	// Check if user is in the list of admins who received approval messages
	_, isValidAdmin := approval.sentMessages[update.CallbackQuery.From.ID]
	if !isValidAdmin {
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

// handleQueueTrackCallback handles both queue track approve and deny callbacks.
func (f *Frontend) handleQueueTrackCallback(ctx context.Context, b *bot.Bot, update *models.Update, approved bool) {
	if update.CallbackQuery == nil {
		return
	}

	trackID := f.extractQueueTrackID(update.CallbackQuery.Data, approved)
	if trackID == "" {
		return
	}

	// Answer the callback query immediately
	var responseText string
	if approved {
		responseText = f.localizer.T("callback.queue_approved")
	} else {
		responseText = f.localizer.T("callback.queue_denied")
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
			f.logger.Debug("Failed to add thumbs up reaction to queue track message", zap.Error(err))
		}

		// Remove the inline keyboard by editing the message
		if _, err := b.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{
			ChatID:    update.CallbackQuery.Message.Message.Chat.ID,
			MessageID: update.CallbackQuery.Message.Message.ID,
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{},
			},
		}); err != nil {
			f.logger.Debug("Failed to remove buttons from queue track message", zap.Error(err))
		}
	} else {
		// Delete the message on denial
		if _, err := b.DeleteMessage(ctx, &bot.DeleteMessageParams{
			ChatID:    update.CallbackQuery.Message.Message.Chat.ID,
			MessageID: update.CallbackQuery.Message.Message.ID,
		}); err != nil {
			f.logger.Debug("Failed to delete queue track message", zap.Error(err))
		}
	}

	// Notify the dispatcher about the decision
	if f.queueTrackDecisionHandler != nil {
		f.queueTrackDecisionHandler(ctx, trackID, approved)
	}

	f.logger.Info("Queue track decision processed",
		zap.String("trackID", trackID),
		zap.Bool("approved", approved),
		zap.String("userID", strconv.FormatInt(update.CallbackQuery.From.ID, 10)))
}

func (f *Frontend) extractQueueTrackID(callbackData string, approved bool) string {
	if approved && strings.HasPrefix(callbackData, "queue_approve_") {
		return strings.TrimPrefix(callbackData, "queue_approve_")
	}
	if !approved && strings.HasPrefix(callbackData, "queue_deny_") {
		return strings.TrimPrefix(callbackData, "queue_deny_")
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

// deleteAdminApprovalMessages deletes all admin approval messages that were sent.
func (f *Frontend) deleteAdminApprovalMessages(ctx context.Context, approval *adminApprovalContext) {
	if approval == nil || len(approval.sentMessages) == 0 {
		return
	}

	f.logger.Debug("Cleaning up admin approval messages",
		zap.Int("message_count", len(approval.sentMessages)))

	for adminID, messageID := range approval.sentMessages {
		_, err := f.bot.DeleteMessage(ctx, &bot.DeleteMessageParams{
			ChatID:    adminID,
			MessageID: messageID,
		})
		if err != nil {
			f.logger.Debug("Failed to delete admin approval message",
				zap.Int64("admin_id", adminID),
				zap.Int("message_id", messageID),
				zap.Error(err))
		} else {
			f.logger.Debug("Deleted admin approval message",
				zap.Int64("admin_id", adminID),
				zap.Int("message_id", messageID))
		}
	}
}

// CancelAdminApproval cancels an ongoing admin approval process and cleans up messages.
func (f *Frontend) CancelAdminApproval(ctx context.Context, origin *chat.Message) {
	// Due to timing, we can't predict the exact timestamp, so we need to search for the approval
	// by checking recent approvals. For now, let's cancel all active admin approvals for this message.
	f.adminApprovalMutex.Lock()
	defer f.adminApprovalMutex.Unlock()

	// Look for admin approvals from this message (approximate match)
	baseKey := fmt.Sprintf("admin_%s_%s_", origin.ChatID, origin.ID)
	for key, approval := range f.pendingAdminApprovals {
		if strings.HasPrefix(key, baseKey) {
			f.logger.Debug("Canceling admin approval due to community approval success",
				zap.String("approval_key", key))

			// Delete the admin approval messages
			f.deleteAdminApprovalMessages(ctx, approval)

			// Cancel the approval context
			approval.cancelFunc()

			// Remove from pending approvals
			delete(f.pendingAdminApprovals, key)
		}
	}
}

func (f *Frontend) isUserAdmin(userID int64, adminList []int64) bool {
	for _, adminID := range adminList {
		if userID == adminID {
			return true
		}
	}
	return false
}

// IsUserAdmin implements the chat.Frontend interface to check if a user is an admin.
func (f *Frontend) IsUserAdmin(ctx context.Context, chatID, userID string) (bool, error) {
	if !f.config.Enabled {
		return false, errors.New("telegram frontend is disabled")
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

// GetAdminUserIDs implements the chat.Frontend interface to get admin user IDs as strings.
func (f *Frontend) GetAdminUserIDs(ctx context.Context, chatID string) ([]string, error) {
	if !f.config.Enabled {
		return nil, errors.New("telegram frontend is disabled")
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

// SendDirectMessage implements the chat.Frontend interface to send direct messages to users.
func (f *Frontend) SendDirectMessage(ctx context.Context, userID, message string) (string, error) {
	if !f.config.Enabled {
		return "", errors.New("telegram frontend is disabled")
	}

	// Parse user ID
	userIDInt, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid user ID: %w", err)
	}

	params := &bot.SendMessageParams{
		ChatID: userIDInt,
		Text:   message,
	}

	// Disable link previews for direct messages
	disabled := true
	params.LinkPreviewOptions = &models.LinkPreviewOptions{
		IsDisabled: &disabled,
	}

	result, err := f.sendMessageWithMigrationHandling(ctx, params)
	if err != nil {
		return "", fmt.Errorf("failed to send direct message to user %s: %w", userID, err)
	}

	msgID := strconv.Itoa(result.ID)

	f.logger.Debug("Sent direct message to user",
		zap.String("userID", userID),
		zap.String("messageID", msgID),
		zap.String("text", message))

	return msgID, nil
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
		messageText := fmt.Sprintf("🎵 Admin Approval: %s\n\nUser: %s\nSong: %s\n\n%s",
			responseText, approval.originUserName, approval.songInfo, responseText)

		if _, err := b.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    update.CallbackQuery.From.ID,
			MessageID: update.CallbackQuery.Message.Message.ID,
			Text:      messageText,
		}); err != nil {
			f.logger.Debug("Failed to edit admin approval message", zap.Error(err))
		}
	}
}

// GroupInfo represents a Telegram group/chat information.
type GroupInfo struct {
	ID    int64
	Title string
	Type  string
}

// ListAvailableGroups returns a list of groups the bot is part of.
func (f *Frontend) ListAvailableGroups(ctx context.Context) ([]GroupInfo, error) {
	groupsMap := make(map[int64]GroupInfo)
	tempHandler := f.createGroupDiscoveryHandler(groupsMap)

	tempBot, err := f.createTemporaryBot(tempHandler)
	if err != nil {
		return nil, err
	}

	groups, err := f.discoverGroups(ctx, tempBot, groupsMap)
	if err != nil {
		return nil, err
	}

	f.logDiscoveryResults(groups)
	return groups, nil
}

// createGroupDiscoveryHandler creates a handler function for group discovery.
func (f *Frontend) createGroupDiscoveryHandler(
	groupsMap map[int64]GroupInfo) func(context.Context, *bot.Bot, *models.Update) {
	return func(_ context.Context, _ *bot.Bot, update *models.Update) {
		f.logger.Info("Received update during group discovery")

		chatModel, hasChat := f.extractChatFromUpdate(update)
		if !hasChat {
			return
		}

		f.processChatForDiscovery(&chatModel, groupsMap)
	}
}

// extractChatFromUpdate extracts chat information from an update.
func (f *Frontend) extractChatFromUpdate(update *models.Update) (models.Chat, bool) {
	var chatModel models.Chat
	var hasChat bool

	if update.Message != nil {
		chatModel = update.Message.Chat
		hasChat = true
		f.logger.Info("Found message in chat",
			zap.Int64("chatID", chatModel.ID),
			zap.String("chatTitle", chatModel.Title),
			zap.String("chatType", string(chatModel.Type)))
	} else if update.CallbackQuery != nil && update.CallbackQuery.Message.Message != nil {
		chatModel = update.CallbackQuery.Message.Message.Chat
		hasChat = true
		f.logger.Info("Found callback query in chat",
			zap.Int64("chatID", chatModel.ID),
			zap.String("chatTitle", chatModel.Title),
			zap.String("chatType", string(chatModel.Type)))
	}

	return chatModel, hasChat
}

// processChatForDiscovery processes a discovered chat and adds it to the groups map if it's a group.
func (f *Frontend) processChatForDiscovery(chatModel *models.Chat, groupsMap map[int64]GroupInfo) {
	if string(chatModel.Type) == chatTypeGroup || string(chatModel.Type) == chatTypeSuperGroup {
		f.logger.Info("Discovered group during scan",
			zap.Int64("groupID", chatModel.ID),
			zap.String("groupTitle", chatModel.Title),
			zap.String("groupType", string(chatModel.Type)))
		groupsMap[chatModel.ID] = GroupInfo{
			ID:    chatModel.ID,
			Title: chatModel.Title,
			Type:  string(chatModel.Type),
		}
	} else {
		f.logger.Info("Ignoring non-group chat",
			zap.Int64("chatID", chatModel.ID),
			zap.String("chatType", string(chatModel.Type)))
	}
}

// createTemporaryBot creates a temporary bot for group discovery.
func (f *Frontend) createTemporaryBot(tempHandler func(context.Context, *bot.Bot, *models.Update)) (*bot.Bot, error) {
	tempBot, err := bot.New(f.config.BotToken, bot.WithDefaultHandler(tempHandler))
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary bot for group discovery: %w", err)
	}

	f.logger.Info("Created temporary bot for group discovery")
	return tempBot, nil
}

// discoverGroups starts the discovery process and waits for results.
func (f *Frontend) discoverGroups(ctx context.Context, tempBot *bot.Bot,
	groupsMap map[int64]GroupInfo) ([]GroupInfo, error) {
	discoverCtx, cancelDiscover := context.WithTimeout(ctx, groupDiscoveryTimeout*time.Second)
	defer cancelDiscover()

	f.startBotPolling(discoverCtx, tempBot)
	f.logDiscoveryInstructions()

	if err := f.waitForDiscovery(ctx, discoverCtx, cancelDiscover); err != nil {
		return nil, err
	}

	return f.convertMapToSlice(groupsMap), nil
}

// startBotPolling starts the bot polling in the background.
func (f *Frontend) startBotPolling(discoverCtx context.Context, tempBot *bot.Bot) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				f.logger.Debug("Bot polling stopped", zap.Any("reason", r))
			}
		}()
		tempBot.Start(discoverCtx)
	}()
}

// logDiscoveryInstructions logs instructions for group discovery.
func (f *Frontend) logDiscoveryInstructions() {
	f.logger.Info("Scanning for groups... Please send a message in any group the bot should monitor")
	f.logger.Info("Waiting 15 seconds for group discovery...")
}

// waitForDiscovery waits for the discovery timeout or context cancellation.
func (f *Frontend) waitForDiscovery(ctx context.Context, _ context.Context,
	cancelDiscover context.CancelFunc) error {
	select {
	case <-time.After(groupDiscoveryTimeout * time.Second):
		f.suppressBotErrors(cancelDiscover)
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// suppressBotErrors temporarily suppresses stderr to hide expected bot errors.
func (f *Frontend) suppressBotErrors(cancelDiscover context.CancelFunc) {
	originalOutput := log.Writer()
	log.SetOutput(io.Discard)

	cancelDiscover()
	time.Sleep(botStopDelay)

	log.SetOutput(originalOutput)
}

// convertMapToSlice converts the groups map to a slice.
func (f *Frontend) convertMapToSlice(groupsMap map[int64]GroupInfo) []GroupInfo {
	groups := make([]GroupInfo, 0, len(groupsMap))
	for _, group := range groupsMap {
		groups = append(groups, group)
	}
	return groups
}

// logDiscoveryResults logs the results of group discovery.
func (f *Frontend) logDiscoveryResults(groups []GroupInfo) {
	f.logger.Info("Group discovery completed",
		zap.Int("groupCount", len(groups)),
		zap.Any("groups", groups))
	time.Sleep(discoveryFinalWait)
}

// AwaitCommunityApproval waits for enough community 👍 reactions to bypass admin approval.
func (f *Frontend) AwaitCommunityApproval(ctx context.Context, msgID string, requiredReactions, timeoutSec int,
	requesterUserID int64) (bool, error) {
	if !f.config.Enabled {
		return false, errors.New("telegram frontend is disabled")
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
		requesterUserID:   requesterUserID,
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

// SendQueueTrackApproval sends a queue track approval message with approve/deny buttons.
func (f *Frontend) SendQueueTrackApproval(ctx context.Context, chatID, trackID, message string) (string, error) {
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
				Text:         f.localizer.T("button.queue_approve"),
				CallbackData: "queue_approve_" + trackID,
			},
			{
				Text:         f.localizer.T("button.queue_deny"),
				CallbackData: "queue_deny_" + trackID,
			},
		},
	}

	// Disable link previews for queue track messages since they contain Spotify links
	disabled := true
	sentMsg, err := f.sendMessageWithMigrationHandling(ctx, &bot.SendMessageParams{
		ChatID:             chatIDInt,
		Text:               message,
		ReplyMarkup:        models.InlineKeyboardMarkup{InlineKeyboard: keyboard},
		LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: &disabled},
	})
	if err != nil {
		return "", fmt.Errorf("failed to send queue track approval message: %w", err)
	}

	f.logger.Debug("Sent queue track approval message",
		zap.String("chatID", chatID),
		zap.String("trackID", trackID),
		zap.Int("messageID", sentMsg.ID))

	return strconv.Itoa(sentMsg.ID), nil
}

// SetQueueTrackDecisionHandler sets the handler for queue track approval/denial decisions.
func (f *Frontend) SetQueueTrackDecisionHandler(handler func(ctx context.Context, trackID string, approved bool)) {
	f.queueTrackDecisionHandler = handler
}

// EditMessage edits an existing message by ID.
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

// GetMe returns information about the bot user.
func (f *Frontend) GetMe(ctx context.Context) (*chat.User, error) {
	if !f.config.Enabled {
		return nil, errors.New("telegram frontend is disabled")
	}

	me, err := f.bot.GetMe(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get bot user info: %w", err)
	}

	return &chat.User{
		ID:        me.ID,
		IsBot:     me.IsBot,
		FirstName: me.FirstName,
		LastName:  me.LastName,
		Username:  me.Username,
	}, nil
}

// GetChatMember returns information about a chat member.
func (f *Frontend) GetChatMember(ctx context.Context, chatID, userID int64) (*chat.ChatMember, error) {
	if !f.config.Enabled {
		return nil, errors.New("telegram frontend is disabled")
	}

	member, err := f.bot.GetChatMember(ctx, &bot.GetChatMemberParams{
		ChatID: chatID,
		UserID: userID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get chat member: %w", err)
	}

	return f.convertToChatMember(member), nil
}

// convertToChatMember converts a Telegram ChatMember to our internal ChatMember structure.
func (f *Frontend) convertToChatMember(member *models.ChatMember) *chat.ChatMember {
	chatMember := &chat.ChatMember{
		Status: string(member.Type),
	}

	switch member.Type {
	case models.ChatMemberTypeOwner:
		f.populateOwnerMember(chatMember, member.Owner)
	case models.ChatMemberTypeAdministrator:
		f.populateAdministratorMember(chatMember, member.Administrator)
	case models.ChatMemberTypeMember:
		f.populateRegularMember(chatMember, member.Member)
	case models.ChatMemberTypeRestricted:
		f.populateRestrictedMember(chatMember, member.Restricted)
	case models.ChatMemberTypeLeft:
		f.populateLeftMember(chatMember, member.Left)
	case models.ChatMemberTypeBanned:
		f.populateBannedMember(chatMember, member.Banned)
	}

	return chatMember
}

// convertUser converts a Telegram User to our internal User structure.
func (f *Frontend) convertUser(telegramUser *models.User) *chat.User {
	if telegramUser == nil {
		return nil
	}
	return &chat.User{
		ID:        telegramUser.ID,
		IsBot:     telegramUser.IsBot,
		FirstName: telegramUser.FirstName,
		LastName:  telegramUser.LastName,
		Username:  telegramUser.Username,
	}
}

// populateOwnerMember populates owner-specific member information.
func (f *Frontend) populateOwnerMember(chatMember *chat.ChatMember, owner *models.ChatMemberOwner) {
	if owner != nil {
		chatMember.User = f.convertUser(owner.User)
		chatMember.IsAnonymous = owner.IsAnonymous
	}
}

// populateAdministratorMember populates administrator-specific member information.
func (f *Frontend) populateAdministratorMember(chatMember *chat.ChatMember, admin *models.ChatMemberAdministrator) {
	if admin != nil {
		chatMember.User = f.convertUser(&admin.User)
		chatMember.CanDeleteMessages = admin.CanDeleteMessages
		chatMember.CanRestrictMembers = admin.CanRestrictMembers
		chatMember.CanPromoteMembers = admin.CanPromoteMembers
		chatMember.CanChangeInfo = admin.CanChangeInfo
		chatMember.CanInviteUsers = admin.CanInviteUsers
		chatMember.CanPinMessages = admin.CanPinMessages
		chatMember.CanManageVideoChats = admin.CanManageVideoChats
		chatMember.IsAnonymous = admin.IsAnonymous
		chatMember.CanManageChat = admin.CanManageChat
		chatMember.CanPostMessages = admin.CanPostMessages
		chatMember.CanEditMessages = admin.CanEditMessages
	}
}

// populateRegularMember populates regular member information.
func (f *Frontend) populateRegularMember(chatMember *chat.ChatMember, regular *models.ChatMemberMember) {
	if regular != nil {
		chatMember.User = f.convertUser(regular.User)
	}
}

// populateRestrictedMember populates restricted member information.
func (f *Frontend) populateRestrictedMember(chatMember *chat.ChatMember, restricted *models.ChatMemberRestricted) {
	if restricted != nil {
		chatMember.User = f.convertUser(restricted.User)
	}
}

// populateLeftMember populates left member information.
func (f *Frontend) populateLeftMember(chatMember *chat.ChatMember, left *models.ChatMemberLeft) {
	if left != nil {
		chatMember.User = f.convertUser(left.User)
	}
}

// populateBannedMember populates banned member information.
func (f *Frontend) populateBannedMember(chatMember *chat.ChatMember, banned *models.ChatMemberBanned) {
	if banned != nil {
		chatMember.User = f.convertUser(banned.User)
	}
}

// isServiceMessage checks if a message is a service message that should be ignored.
func (f *Frontend) isServiceMessage(msg *models.Message) bool {
	// Check for member-related service messages
	if len(msg.NewChatMembers) > 0 || msg.LeftChatMember != nil {
		return true
	}

	// Check for chat setting changes
	if msg.NewChatTitle != "" || msg.NewChatPhoto != nil || msg.DeleteChatPhoto {
		return true
	}

	// Check for group creation events
	if msg.GroupChatCreated || msg.SupergroupChatCreated || msg.ChannelChatCreated {
		return true
	}

	// Check for other service message types.
	if msg.MessageAutoDeleteTimerChanged != nil {
		return true
	}

	return false
}

// getServiceMessageType returns a description of the service message type for logging.
func (f *Frontend) getServiceMessageType(msg *models.Message) string {
	if len(msg.NewChatMembers) > 0 {
		return "new_chat_members"
	}
	if msg.LeftChatMember != nil {
		return "left_chat_member"
	}
	if msg.NewChatTitle != "" {
		return "new_chat_title"
	}
	if msg.NewChatPhoto != nil {
		return "new_chat_photo"
	}
	if msg.DeleteChatPhoto {
		return "delete_chat_photo"
	}
	if msg.GroupChatCreated {
		return "group_chat_created"
	}
	if msg.SupergroupChatCreated {
		return "supergroup_chat_created"
	}
	if msg.ChannelChatCreated {
		return "channel_chat_created"
	}
	if msg.MessageAutoDeleteTimerChanged != nil {
		return "message_auto_delete_timer_changed"
	}
	return "unknown_service_message"
}

// extractMigrateToChatID parses Telegram migration errors and returns the new supergroup chat ID.
// When a group is upgraded to a supergroup, Telegram returns an error with the new chat ID.
// Returns (newChatID, true) if migration detected, (0, false) otherwise.
func extractMigrateToChatID(err error) (int64, bool) {
	if err == nil {
		return 0, false
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "migrate_to_chat_id") {
		return 0, false
	}

	// Parse pattern: "group chat was upgraded to a supergroup chat: migrate_to_chat_id -1003240660387"
	// Extract the numeric chat ID after "migrate_to_chat_id"
	const migrationPrefix = "migrate_to_chat_id "
	idx := strings.Index(errStr, migrationPrefix)
	if idx == -1 {
		return 0, false
	}

	// Get substring after prefix
	chatIDStr := errStr[idx+len(migrationPrefix):]

	// Find first space or end of string
	endIdx := strings.IndexAny(chatIDStr, " \t\n\r")
	if endIdx != -1 {
		chatIDStr = chatIDStr[:endIdx]
	}

	// Parse the chat ID
	chatID, parseErr := strconv.ParseInt(chatIDStr, 10, 64)
	if parseErr != nil {
		return 0, false
	}

	return chatID, true
}

// sendMessageWithMigrationHandling wraps bot.SendMessage with automatic chat migration handling.
// If the chat has been upgraded to a supergroup, it automatically updates the chat ID and retries.
func (f *Frontend) sendMessageWithMigrationHandling(
	ctx context.Context,
	params *bot.SendMessageParams,
) (*models.Message, error) {
	msg, err := f.bot.SendMessage(ctx, params)
	if err == nil {
		return msg, nil
	}

	// Check if this is a migration error
	newChatID, isMigration := extractMigrateToChatID(err)
	if !isMigration {
		// Not a migration error, return original error
		return nil, err
	}

	// Handle migration: extract old chat ID for logging
	var oldChatID int64
	switch v := params.ChatID.(type) {
	case int64:
		oldChatID = v
	case string:
		oldChatID, _ = strconv.ParseInt(v, 10, 64)
	}

	f.logger.Info("Detected chat migration, updating chat ID and retrying",
		zap.Int64("old_chat_id", oldChatID),
		zap.Int64("new_chat_id", newChatID))

	// Update the chat ID in our config
	f.config.GroupID = newChatID

	// Also update core config's GroupID if pointer was provided
	if f.coreGroupIDPtr != nil {
		*f.coreGroupIDPtr = newChatID
		f.logger.Info("Updated core config GroupID after migration",
			zap.Int64("new_group_id", newChatID))
	}

	// Retry with the new chat ID
	params.ChatID = newChatID
	retryMsg, retryErr := f.bot.SendMessage(ctx, params)
	if retryErr != nil {
		return nil, fmt.Errorf("failed to send message after migration: %w", retryErr)
	}
	return retryMsg, nil
}
