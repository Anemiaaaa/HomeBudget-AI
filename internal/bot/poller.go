package bot

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/expense"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/telegram"
)

var ErrWebhookConfigured = errors.New("Telegram webhook is configured; long polling is disabled")

type PollingTelegram interface {
	GetWebhookInfo(context.Context) (telegram.WebhookInfo, error)
	GetUpdates(context.Context, int64, int) ([]telegram.Update, error)
}

type Inbox interface {
	NextOffset(context.Context) (int64, error)
	Ingest(context.Context, []IncomingUpdate, int64) (int64, error)
}

type Poller struct {
	telegram       PollingTelegram
	inbox          Inbox
	location       *time.Location
	allowedUserIDs map[int64]struct{}
	timeoutSeconds int
}

func NewPoller(telegramClient PollingTelegram, inbox Inbox, location *time.Location, allowedUserIDs map[int64]struct{}) *Poller {
	return &Poller{
		telegram: telegramClient, inbox: inbox, location: location,
		allowedUserIDs: allowedUserIDs, timeoutSeconds: 25,
	}
}

func (p *Poller) CheckWebhook(ctx context.Context) error {
	info, err := p.telegram.GetWebhookInfo(ctx)
	if err != nil {
		return err
	}
	if info.URL != "" {
		return ErrWebhookConfigured
	}
	return nil
}

func (p *Poller) PollOnce(ctx context.Context) (int64, error) {
	if p.telegram == nil || p.inbox == nil || p.location == nil {
		return 0, errors.New("bot poller is not configured")
	}
	offset, err := p.inbox.NextOffset(ctx)
	if err != nil {
		return 0, err
	}
	updates, err := p.telegram.GetUpdates(ctx, offset, p.timeoutSeconds)
	if err != nil {
		return 0, err
	}
	if len(updates) == 0 {
		return 0, nil
	}
	incoming := make([]IncomingUpdate, 0, len(updates))
	nextOffset := offset
	for _, update := range updates {
		if update.UpdateID < 0 {
			return 0, fmt.Errorf("Telegram returned invalid update ID %d", update.UpdateID)
		}
		if update.UpdateID >= nextOffset {
			nextOffset = update.UpdateID + 1
		}
		incoming = append(incoming, p.mapUpdate(update))
	}
	return p.inbox.Ingest(ctx, incoming, nextOffset)
}

func (p *Poller) mapUpdate(update telegram.Update) IncomingUpdate {
	ignored := IncomingUpdate{UpdateID: update.UpdateID, Status: StatusIgnored}
	message := update.Message
	if message == nil || message.From == nil || message.From.ID <= 0 || message.Chat.ID == 0 || message.Chat.Type != "private" || message.MessageID <= 0 || message.Date <= 0 || message.Text == "" {
		return ignored
	}
	if len(p.allowedUserIDs) > 0 {
		if _, allowed := p.allowedUserIDs[message.From.ID]; !allowed {
			return ignored
		}
	}
	messageAt := time.Unix(message.Date, 0).UTC()
	referenceDate := expense.DateInLocation(messageAt, p.location)
	userID, chatID, messageID, text := message.From.ID, message.Chat.ID, message.MessageID, message.Text
	return IncomingUpdate{
		UpdateID: update.UpdateID, UserID: &userID, ChatID: &chatID, MessageID: &messageID,
		MessageAt: &messageAt, ReferenceDate: &referenceDate, Text: &text, Status: StatusPending,
	}
}
