// Package whatsapp provides WhatsApp client integration using whatsmeow library.
package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
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

	"whatdj/internal/chat"
	"whatdj/internal/i18n"
	"whatdj/pkg/text"
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

// handleReactionEvent processes reaction events
// Currently unused but kept for future implementation
//
//nolint:unused
func (f *Frontend) handleReactionEvent(evt *events.Receipt) {
	// Find matching approval context based on message ID
	var matchingApproval *approvalContext

	f.approvalMutex.RLock()
	for key, approval := range f.pendingApprovals {
		// This is simplified - in practice you'd need to match the message ID properly
		if strings.Contains(key, evt.MessageIDs[0]) && approval.originUserID == evt.SourceString() {
			matchingApproval = approval
			_ = key // Avoid unused variable warning
			break
		}
	}
	f.approvalMutex.RUnlock()

	if matchingApproval == nil {
		return
	}

	// Determine if this is approval or rejection
	// Note: WhatsApp receipt events don't carry the actual reaction emoji
	// This would need to be implemented differently in a real system
	approved := true // Simplified - assume any reaction is approval

	select {
	case matchingApproval.approved <- approved:
	case <-matchingApproval.cancelCtx.Done():
	}
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
