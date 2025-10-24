// Package whatsapp provides WhatsApp client integration using whatsmeow library.
package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"time"

	// SQLite driver for whatsmeow session storage
	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.uber.org/zap"

	"github.com/mdp/qrterminal/v3"

	"djalgorhythm/internal/chat"
	"djalgorhythm/internal/i18n"
	"djalgorhythm/pkg/text"
)

// Config holds WhatsApp-specific configuration
type Config struct {
	GroupJID    string
	DeviceName  string
	SessionPath string
	Enabled     bool
	Language    string // Bot language for user-facing messages
}

// Frontend implements the chat.Frontend interface for WhatsApp
type Frontend struct {
	config    *Config
	logger    *zap.Logger
	client    *whatsmeow.Client
	container *sqlstore.Container
	parser    *text.Parser
	localizer *i18n.Localizer

	// Message handling
	messageHandler func(*chat.Message)

	// Reaction and approval tracking
	approvalMutex    sync.RWMutex
	pendingApprovals map[string]*approvalContext
}

// approvalContext tracks pending user approvals
type approvalContext struct {
	originUserID string
	approved     chan bool
	cancelCtx    context.Context
	cancelFunc   context.CancelFunc
}

// NewFrontend creates a new WhatsApp frontend
func NewFrontend(config *Config, logger *zap.Logger) *Frontend {
	// Use configured language, fallback to default if not set
	language := config.Language
	if language == "" {
		language = i18n.DefaultLanguage
	}

	return &Frontend{
		config:           config,
		logger:           logger,
		parser:           text.NewParser(),
		localizer:        i18n.NewLocalizer(language),
		pendingApprovals: make(map[string]*approvalContext),
	}
}

// Start initializes the WhatsApp client and begins listening for updates
func (f *Frontend) Start(ctx context.Context) error {
	if !f.config.Enabled {
		f.logger.Warn("⚠️  WhatsApp bot mode is disabled by default (may violate ToS). Enable at your own risk.")
		return nil
	}

	f.logger.Info("Starting WhatsApp frontend")

	if err := f.initDatabase(); err != nil {
		return fmt.Errorf("failed to init database: %w", err)
	}

	if err := f.initClient(); err != nil {
		return fmt.Errorf("failed to init client: %w", err)
	}

	f.client.AddEventHandler(f.handleEvent)

	if f.client.Store.ID == nil {
		qrChan, _ := f.client.GetQRChannel(ctx)
		if err := f.client.Connect(); err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}

		for evt := range qrChan {
			if evt.Event == "code" {
				f.logger.Info("QR code received, please scan with your phone")
				fmt.Println("QR Code:")
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
			} else {
				f.logger.Info("Login event", zap.String("event", evt.Event))
			}
		}
	} else {
		if err := f.client.Connect(); err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
	}

	f.logger.Info("WhatsApp frontend started successfully")
	return nil
}

// Listen starts listening for messages and calls the handler for each message
func (f *Frontend) Listen(ctx context.Context, handler func(*chat.Message)) error {
	if !f.config.Enabled {
		return nil // Do nothing if disabled
	}

	f.messageHandler = handler

	// WhatsApp client runs in background via event handlers
	// Just wait for context cancellation
	<-ctx.Done()

	return f.stop(ctx)
}

// SendText sends a text message to the specified chat, optionally as a reply
func (f *Frontend) SendText(ctx context.Context, chatID, replyToID, text string) (string, error) {
	if !f.config.Enabled {
		return "", fmt.Errorf("whatsapp frontend is disabled")
	}

	jid, err := types.ParseJID(chatID)
	if err != nil {
		return "", fmt.Errorf("invalid chat JID: %w", err)
	}

	var msg *waE2E.Message

	if replyToID != "" {
		msg = &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text: &text,
				ContextInfo: &waE2E.ContextInfo{
					StanzaID:    &replyToID,
					Participant: &[]string{jid.String()}[0],
				},
			},
		}
	} else {
		msg = &waE2E.Message{
			Conversation: &text,
		}
	}

	resp, err := f.client.SendMessage(ctx, jid, msg)
	if err != nil {
		return "", fmt.Errorf("failed to send message: %w", err)
	}

	return resp.ID, nil
}

// React adds an emoji reaction to a message
func (f *Frontend) React(ctx context.Context, chatID, msgID string, r chat.Reaction) error {
	if !f.config.Enabled {
		return fmt.Errorf("whatsapp frontend is disabled")
	}

	jid, err := types.ParseJID(chatID)
	if err != nil {
		return fmt.Errorf("invalid chat JID: %w", err)
	}

	senderJID, err := types.ParseJID(chatID) // For group messages, this would need to be the actual sender
	if err != nil {
		return fmt.Errorf("invalid sender JID: %w", err)
	}

	reactionMsg := f.client.BuildReaction(jid, senderJID, msgID, string(r))
	_, err = f.client.SendMessage(ctx, jid, reactionMsg)
	return err
}

// DeleteMessage deletes a message by its ID
// Note: WhatsApp doesn't support message deletion for bots in the same way as Telegram
func (f *Frontend) DeleteMessage(_ context.Context, _, _ string) error {
	if !f.config.Enabled {
		return fmt.Errorf("whatsapp frontend is disabled")
	}
	// WhatsApp doesn't support message deletion via bot API
	// This is a stub implementation for interface compatibility
	return nil
}

// AwaitApproval waits for user approval via reaction
// Note: WhatsApp primarily uses reactions for approval
func (f *Frontend) AwaitApproval(ctx context.Context, origin *chat.Message, prompt string, timeoutSec int) (bool, error) {
	if !f.config.Enabled {
		return false, fmt.Errorf("whatsapp frontend is disabled")
	}

	// Create approval context
	approvalCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	approval := &approvalContext{
		originUserID: origin.SenderID,
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

	// Send the prompt message
	_, err := f.SendText(ctx, origin.ChatID, origin.ID, prompt)
	if err != nil {
		return false, fmt.Errorf("failed to send approval prompt: %w", err)
	}

	// Wait for approval or timeout
	select {
	case approved := <-approval.approved:
		return approved, nil
	case <-approvalCtx.Done():
		return false, nil
	}
}

// AwaitCommunityApproval waits for enough community 👍 reactions to bypass admin approval
// WhatsApp doesn't support reactions like Telegram, so this always returns false
func (f *Frontend) AwaitCommunityApproval(_ context.Context, _ string, _, _ int, _ int64) (bool, error) {
	if !f.config.Enabled {
		return false, fmt.Errorf("whatsapp frontend is disabled")
	}

	// WhatsApp doesn't support reactions like Telegram, so community approval is not supported
	return false, nil
}

// handleEvent processes incoming WhatsApp events
func (f *Frontend) handleEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		f.handleMessageEvent(v)
	case *events.Receipt:
		f.handleReceiptEvent(v)
	case *events.Presence:
	case *events.HistorySync:
	case *events.AppState:
	case *events.KeepAliveTimeout:
		f.logger.Warn("Received KeepAlive timeout, reconnecting...")
	case *events.KeepAliveRestored:
		f.logger.Info("Connection restored after timeout")
	default:
	}
}

// handleMessageEvent processes incoming messages
func (f *Frontend) handleMessageEvent(evt *events.Message) {
	if evt.Message == nil {
		return
	}

	if evt.Info.Chat.Server != types.GroupServer {
		return
	}

	if f.config.GroupJID != "" && evt.Info.Chat.String() != f.config.GroupJID {
		return
	}

	if evt.Info.IsFromMe {
		return
	}

	text := f.extractMessageText(evt.Message)
	if text == "" {
		return
	}

	// Parse the message using the existing parser
	parsed := f.parser.ParseMessage(text)

	// Convert to unified message format
	message := chat.Message{
		ID:         evt.Info.ID,
		ChatID:     evt.Info.Chat.String(),
		SenderID:   evt.Info.Sender.String(),
		SenderName: evt.Info.PushName,
		Text:       text,
		URLs:       parsed.URLs,
		IsGroup:    true,
		Raw:        evt,
	}

	// Call the message handler
	if f.messageHandler != nil {
		f.messageHandler(&message)
	}
}

// handleReceiptEvent processes receipt events (including reactions)
func (f *Frontend) handleReceiptEvent(_ *events.Receipt) {
	// Handle receipt events - in newer whatsmeow versions, reactions might be handled differently
	// This is a placeholder for potential reaction handling
	// Note: ReceiptTypeReacted may not be available in current whatsmeow version
	// if evt.Type == types.ReceiptTypeReacted {
	//     f.handleReactionEvent(evt)
	// }
}

// stop closes the WhatsApp client connection
func (f *Frontend) stop(_ context.Context) error {
	f.logger.Info("Stopping WhatsApp frontend")

	if f.client != nil {
		f.client.Disconnect()
	}

	if f.container != nil {
		if err := f.container.Close(); err != nil {
			f.logger.Warn("Failed to close whatsapp container", zap.Error(err))
		}
	}

	return nil
}

// initDatabase initializes the SQLite database for session storage
func (f *Frontend) initDatabase() error {
	dsn := fmt.Sprintf("file:%s?_foreign_keys=on", f.config.SessionPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return err
	}

	container := sqlstore.NewWithDB(db, "sqlite3", nil)
	f.container = container
	err = container.Upgrade(context.Background())
	if err != nil {
		return err
	}
	return nil
}

// initClient initializes the WhatsApp client
func (f *Frontend) initClient() error {
	deviceStore, err := f.container.GetFirstDevice(context.Background())
	if err != nil {
		return err
	}

	client := whatsmeow.NewClient(deviceStore, nil)
	f.client = client
	return nil
}

// extractMessageText extracts text content from various WhatsApp message types
func (f *Frontend) extractMessageText(msg *waE2E.Message) string {
	if msg.Conversation != nil {
		return *msg.Conversation
	}

	if msg.ExtendedTextMessage != nil && msg.ExtendedTextMessage.Text != nil {
		return *msg.ExtendedTextMessage.Text
	}

	if msg.ImageMessage != nil && msg.ImageMessage.Caption != nil {
		return *msg.ImageMessage.Caption
	}

	if msg.VideoMessage != nil && msg.VideoMessage.Caption != nil {
		return *msg.VideoMessage.Caption
	}

	if msg.DocumentMessage != nil && msg.DocumentMessage.Caption != nil {
		return *msg.DocumentMessage.Caption
	}

	return ""
}

// IsUserAdmin implements the chat.Frontend interface to check if a user is an admin
// For WhatsApp, this is a stub implementation since WhatsApp group admin checking
// requires more complex API calls that are not easily available through whatsmeow
func (f *Frontend) IsUserAdmin(_ context.Context, chatID, userID string) (bool, error) {
	if !f.config.Enabled {
		return false, fmt.Errorf("whatsapp frontend is disabled")
	}

	// For now, return false - WhatsApp admin checking would require
	// querying group metadata which is complex and not always reliable
	// This could be enhanced in the future if needed
	f.logger.Debug("WhatsApp admin checking not implemented, returning false",
		zap.String("chatID", chatID),
		zap.String("userID", userID))

	return false, nil
}

// GetAdminUserIDs implements the chat.Frontend interface to get admin user IDs
// For WhatsApp, this is a stub implementation since admin detection is not fully implemented
func (f *Frontend) GetAdminUserIDs(_ context.Context, chatID string) ([]string, error) {
	if !f.config.Enabled {
		return nil, fmt.Errorf("whatsapp frontend is disabled")
	}

	// WhatsApp admin detection is not implemented, return empty list
	f.logger.Debug("WhatsApp admin detection not implemented, returning empty admin list",
		zap.String("chatID", chatID))

	return []string{}, nil
}

// SendDirectMessage implements the chat.Frontend interface to send direct messages
// For WhatsApp, this sends a message to the user's phone number as JID
func (f *Frontend) SendDirectMessage(ctx context.Context, userID, text string) (string, error) {
	if !f.config.Enabled {
		return "", fmt.Errorf("whatsapp frontend is disabled")
	}

	if f.client == nil {
		return "", fmt.Errorf("whatsapp client not initialized")
	}

	// Convert userID to WhatsApp JID format (phone number)
	userJID, err := types.ParseJID(userID)
	if err != nil {
		// Try adding @s.whatsapp.net if not already formatted
		userJID, err = types.ParseJID(userID + "@s.whatsapp.net")
		if err != nil {
			return "", fmt.Errorf("invalid user ID format: %w", err)
		}
	}

	message := &waE2E.Message{
		Conversation: &text,
	}

	result, err := f.client.SendMessage(ctx, userJID, message)
	if err != nil {
		return "", fmt.Errorf("failed to send direct message to user %s: %w", userID, err)
	}

	msgID := result.ID

	f.logger.Debug("Sent direct message to user",
		zap.String("userID", userID),
		zap.String("userJID", userJID.String()),
		zap.String("messageID", msgID),
		zap.String("text", text))

	return msgID, nil
}

// SendQueueTrackApproval sends a queue track approval message with approve/deny buttons
// WhatsApp doesn't support inline buttons like Telegram, so this falls back to regular text
func (f *Frontend) SendQueueTrackApproval(ctx context.Context, chatID, _ /* trackID */, message string) (string, error) {
	if !f.config.Enabled {
		return "", fmt.Errorf("whatsapp frontend is disabled")
	}

	// WhatsApp doesn't support inline buttons, so we'll send the message as regular text
	// Users would need to reply with specific text or reactions for approval/denial
	fullMessage := message + "\n\n" + f.localizer.T("bot.queue_whatsapp_instructions")

	return f.SendText(ctx, chatID, "", fullMessage)
}

// SetQueueTrackDecisionHandler sets the handler for queue track approval/denial decisions
// This is a stub implementation for WhatsApp since it doesn't support inline buttons
func (f *Frontend) SetQueueTrackDecisionHandler(_ func(trackID string, approved bool)) {
	// WhatsApp doesn't support inline button callbacks like Telegram
	// This would need to be implemented through message parsing or reactions
	// For now, this is just a stub to satisfy the interface
	f.logger.Debug("SetQueueTrackDecisionHandler called on WhatsApp frontend (stub implementation)")
}

// EditMessage edits an existing message by ID
// WhatsApp doesn't support message editing like Telegram, so this is a stub implementation
func (f *Frontend) EditMessage(_ context.Context, _, _, _ string) error {
	if !f.config.Enabled {
		return fmt.Errorf("whatsapp frontend is disabled")
	}

	// WhatsApp doesn't support message editing, so this is a no-op
	// This gracefully satisfies the interface without causing errors
	f.logger.Debug("EditMessage called on WhatsApp frontend (not supported, gracefully ignored)")
	return nil
}

// GetMe returns information about the bot user
func (f *Frontend) GetMe(_ context.Context) (*chat.User, error) {
	if !f.config.Enabled {
		return nil, fmt.Errorf("whatsapp frontend is disabled")
	}

	// WhatsApp doesn't have a traditional "bot" concept like Telegram
	// Return a basic user representation using the connected device info
	f.logger.Debug("GetMe called on WhatsApp frontend (limited support)")
	return &chat.User{
		ID:        0, // WhatsApp doesn't use numeric user IDs
		IsBot:     false,
		FirstName: "WhatsApp Bot",
		LastName:  "",
		Username:  "",
	}, nil
}

// GetChatMember returns information about a chat member
func (f *Frontend) GetChatMember(_ context.Context, _, _ int64) (*chat.ChatMember, error) {
	if !f.config.Enabled {
		return nil, fmt.Errorf("whatsapp frontend is disabled")
	}

	// WhatsApp doesn't have the same admin/member structure as Telegram
	// Return a basic member representation
	f.logger.Debug("GetChatMember called on WhatsApp frontend (limited support)")
	return &chat.ChatMember{
		Status: "member", // WhatsApp doesn't have the same status concepts
		User: &chat.User{
			ID:        0,
			IsBot:     false,
			FirstName: "WhatsApp User",
			LastName:  "",
			Username:  "",
		},
	}, nil
}
