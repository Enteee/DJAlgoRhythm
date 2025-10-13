package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "github.com/rs/zerolog"
	"go.uber.org/zap"

	"whatdj/internal/core"
	"whatdj/pkg/text"
)

type Client struct {
	config          *core.WhatsAppConfig
	logger          *zap.Logger
	client          *whatsmeow.Client
	container       *sqlstore.Container
	parser          *text.Parser
	messageHandler  func(core.InputMessage)
	reactionHandler func(groupJID, senderJID, messageID, reaction string)
}

func NewClient(config *core.WhatsAppConfig, logger *zap.Logger) *Client {
	return &Client{
		config: config,
		logger: logger,
		parser: text.NewParser(),
	}
}

func (c *Client) Start(ctx context.Context) error {
	c.logger.Info("Starting WhatsApp client")

	if err := c.initDatabase(); err != nil {
		return fmt.Errorf("failed to init database: %w", err)
	}

	if err := c.initClient(); err != nil {
		return fmt.Errorf("failed to init client: %w", err)
	}

	c.client.AddEventHandler(c.handleEvent)

	if c.client.Store.ID == nil {
		qrChan, _ := c.client.GetQRChannel(ctx)
		if err := c.client.Connect(); err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}

		for evt := range qrChan {
			if evt.Event == "code" {
				c.logger.Info("QR code received, please scan with your phone")
				fmt.Println("QR Code:")
				fmt.Println(evt.Code)
			} else {
				c.logger.Info("Login event", zap.String("event", evt.Event))
			}
		}
	} else {
		if err := c.client.Connect(); err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
	}

	c.logger.Info("WhatsApp client connected successfully")
	return nil
}

func (c *Client) Stop(ctx context.Context) error {
	c.logger.Info("Stopping WhatsApp client")

	if c.client != nil {
		c.client.Disconnect()
	}

	if c.container != nil {
		c.container.Close()
	}

	return nil
}

func (c *Client) SendMessage(ctx context.Context, groupJID, text string) error {
	jid, err := types.ParseJID(groupJID)
	if err != nil {
		return fmt.Errorf("invalid group JID: %w", err)
	}

	msg := &waProto.Message{
		Conversation: &text,
	}

	_, err = c.client.SendMessage(ctx, jid, msg)
	return err
}

func (c *Client) ReplyToMessage(ctx context.Context, groupJID, messageID, text string) error {
	jid, err := types.ParseJID(groupJID)
	if err != nil {
		return fmt.Errorf("invalid group JID: %w", err)
	}

	msg := &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: &text,
			ContextInfo: &waProto.ContextInfo{
				StanzaID:    &messageID,
				Participant: &[]string{jid.String()}[0],
			},
		},
	}

	_, err = c.client.SendMessage(ctx, jid, msg)
	return err
}

func (c *Client) ReactToMessage(ctx context.Context, groupJID, senderJID, messageID, reaction string) error {
	jid, err := types.ParseJID(groupJID)
	if err != nil {
		return fmt.Errorf("invalid group JID: %w", err)
	}

	senderJIDParsed, err := types.ParseJID(senderJID)
	if err != nil {
		return fmt.Errorf("invalid sender JID: %w", err)
	}

	reactionMsg := c.client.BuildReaction(jid, senderJIDParsed, messageID, reaction)
	_, err = c.client.SendMessage(ctx, jid, reactionMsg)
	return err
}

func (c *Client) SetMessageHandler(handler func(core.InputMessage)) {
	c.messageHandler = handler
}

func (c *Client) SetReactionHandler(handler func(groupJID, senderJID, messageID, reaction string)) {
	c.reactionHandler = handler
}

func (c *Client) initDatabase() error {
	db, err := sql.Open("sqlite3", c.config.SessionPath)
	if err != nil {
		return err
	}

	container := sqlstore.NewWithDB(db, "sqlite3", waLog.Nop())
	c.container = container

	return nil
}

func (c *Client) initClient() error {
	deviceStore, err := c.container.GetFirstDevice(context.Background())
	if err != nil {
		return err
	}

	client := whatsmeow.NewClient(deviceStore, waLog.Nop())
	c.client = client
	return nil
}

func (c *Client) handleEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		c.handleMessageEvent(v)
	case *events.Receipt:
		c.handleReceiptEvent(v)
	case *events.Presence:
	case *events.HistorySync:
	case *events.AppState:
	case *events.KeepAliveTimeout:
		c.logger.Warn("Received KeepAlive timeout, reconnecting...")
	case *events.KeepAliveRestored:
		c.logger.Info("Connection restored after timeout")
	default:
	}
}

func (c *Client) handleMessageEvent(evt *events.Message) {
	if evt.Message == nil {
		return
	}

	if evt.Info.Chat.Server != types.GroupServer {
		return
	}

	if c.config.GroupJID != "" && evt.Info.Chat.String() != c.config.GroupJID {
		return
	}

	if evt.Info.IsFromMe {
		return
	}

	text := c.extractMessageText(evt.Message)
	if text == "" {
		return
	}

	parsed := c.parser.ParseMessage(text)
	parsed.GroupJID = evt.Info.Chat.String()
	parsed.SenderJID = evt.Info.Sender.String()
	parsed.MessageID = evt.Info.ID
	parsed.Timestamp = evt.Info.Timestamp

	if c.messageHandler != nil {
		c.messageHandler(parsed)
	}
}

func (c *Client) handleReceiptEvent(evt *events.Receipt) {
	// Handle receipt events - reaction handling would need different approach in newer whatsmeow
}

func (c *Client) extractMessageText(msg *waProto.Message) string {
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