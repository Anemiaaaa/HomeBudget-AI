package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/telegram"
)

type telegramPollingStub struct {
	webhook telegram.WebhookInfo
	updates []telegram.Update
	offset  int64
	timeout int
}

func (s *telegramPollingStub) GetWebhookInfo(context.Context) (telegram.WebhookInfo, error) {
	return s.webhook, nil
}

func (s *telegramPollingStub) GetUpdates(_ context.Context, offset int64, timeout int) ([]telegram.Update, error) {
	s.offset, s.timeout = offset, timeout
	return s.updates, nil
}

type inboxStub struct {
	offset     int64
	updates    []IncomingUpdate
	nextOffset int64
}

func (s *inboxStub) NextOffset(context.Context) (int64, error) { return s.offset, nil }
func (s *inboxStub) Ingest(_ context.Context, updates []IncomingUpdate, nextOffset int64) (int64, error) {
	s.updates, s.nextOffset = updates, nextOffset
	return int64(len(updates)), nil
}

func TestPollerMapsPrivateAllowedMessagesAndAdvancesOffset(t *testing.T) {
	t.Parallel()
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	messageTime := time.Date(2026, 10, 15, 21, 30, 0, 0, time.UTC).Unix()
	telegramStub := &telegramPollingStub{updates: []telegram.Update{
		{UpdateID: 10, Message: &telegram.Message{MessageID: 1, From: &telegram.User{ID: 101}, Chat: telegram.Chat{ID: 501, Type: "private"}, Date: messageTime, Text: "кофе 350"}},
		{UpdateID: 11, Message: &telegram.Message{MessageID: 2, From: &telegram.User{ID: 202}, Chat: telegram.Chat{ID: 502, Type: "private"}, Date: messageTime, Text: "такси 500"}},
		{UpdateID: 12, Message: &telegram.Message{MessageID: 3, From: &telegram.User{ID: 101}, Chat: telegram.Chat{ID: -10, Type: "group"}, Date: messageTime, Text: "еда 100"}},
	}}
	inbox := &inboxStub{offset: 10}
	poller := NewPoller(telegramStub, inbox, location, map[int64]struct{}{101: {}})

	inserted, err := poller.PollOnce(context.Background())
	if err != nil || inserted != 3 {
		t.Fatalf("PollOnce() = %d, %v", inserted, err)
	}
	if telegramStub.offset != 10 || telegramStub.timeout != 25 || inbox.nextOffset != 13 || len(inbox.updates) != 3 {
		t.Fatalf("telegram = %+v, inbox = %+v", telegramStub, inbox)
	}
	first := inbox.updates[0]
	if first.Status != StatusPending || first.UserID == nil || *first.UserID != 101 || first.ReferenceDate == nil || first.ReferenceDate.String() != "2026-10-16" {
		t.Fatalf("mapped allowed update = %+v", first)
	}
	if inbox.updates[1].Status != StatusIgnored || inbox.updates[2].Status != StatusIgnored {
		t.Fatalf("ignored updates = %+v", inbox.updates[1:])
	}
}

func TestPollerRefusesConfiguredWebhook(t *testing.T) {
	t.Parallel()
	poller := NewPoller(&telegramPollingStub{webhook: telegram.WebhookInfo{URL: "https://example.test/hook"}}, &inboxStub{}, time.UTC, nil)
	if err := poller.CheckWebhook(context.Background()); !errors.Is(err, ErrWebhookConfigured) {
		t.Fatalf("CheckWebhook() error = %v", err)
	}
}
