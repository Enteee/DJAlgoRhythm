// Package telegram provides Telegram Bot API integration using go-telegram/bot library.
package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.uber.org/zap"

	"whatdj/internal/chat"
	"whatdj/pkg/text"
)

const (
	entityTypeURL = "url"
)

// Config holds Telegram-specific configuration
type Config struct {
	BotToken        string
	GroupID         int64  // Chat ID of the group to monitor
	GroupName       string // Optional: group name for display purposes
	Enabled         bool
	ReactionSupport bool // Whether the group supports reactions
}

// Frontend implements the chat.Frontend interface for Telegram
type Frontend struct {
	config *Config
	logger *zap.Logger
	bot    *bot.Bot
	parser *text.Parser

	// Message handling
	messageHandler func(*chat.Message)

	// Approval tracking
	approvalMutex    sync.RWMutex
	pendingApprovals map[string]*approvalContext
}

// approvalContext tracks pending user approvals
type approvalContext struct {
	originUserID int64
	approved     chan bool
	cancelCtx    context.Context
	cancelFunc   context.CancelFunc
}

// NewFrontend creates a new Telegram frontend
func NewFrontend(config *Config, logger *zap.Logger) *Frontend {
	return &Frontend{
		config:           config,
		logger:           logger,
		parser:           text.NewParser(),
		pendingApprovals: make(map[string]*approvalContext),
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
	}

	b, err := bot.New(f.config.BotToken, opts...)
	if err != nil {
		return fmt.Errorf("failed to create telegram bot: %w", err)
	}

	f.bot = b

	// Verify bot can access the group
	if err := f.verifyGroupAccess(ctx); err != nil {
		return fmt.Errorf("failed to verify group access: %w", err)
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
				Text:         "👍 Confirm",
				CallbackData: "confirm_" + approvalKey,
			},
			{
				Text:         "👎 Not this",
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
		if _, delErr := f.bot.DeleteMessage(ctx, &bot.DeleteMessageParams{
			ChatID:    chatIDInt,
			MessageID: promptMsgID,
		}); delErr != nil {
			f.logger.Debug("Failed to delete prompt message", zap.Error(delErr))
		}
		return approved, nil
	case <-approvalCtx.Done():
		// Clean up the prompt message on timeout
		if _, delErr := f.bot.DeleteMessage(ctx, &bot.DeleteMessageParams{
			ChatID:    chatIDInt,
			MessageID: promptMsgID,
		}); delErr != nil {
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
		IsGroup:    msg.Chat.Type == "group" || msg.Chat.Type == "supergroup",
		Raw:        msg,
	}

	// Call the message handler
	if f.messageHandler != nil {
		f.messageHandler(&message)
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
			Text:            "This prompt has expired.",
		}); ansErr != nil {
			f.logger.Debug("Failed to answer callback query", zap.Error(ansErr))
		}
		return
	}

	// Check if the user clicking is the same as the original sender
	if update.CallbackQuery.From.ID != approval.originUserID {
		if _, ansErr := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Only the original sender can respond to this.",
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
			responseText = "✅ Confirmed"
		} else {
			responseText = "❌ Rejected"
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
			Text:            "This prompt has expired.",
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
