package bot

import (
	"encoding/json"
	"time"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/expense"
)

type Status string

const (
	StatusPending        Status = "pending"
	StatusProcessing     Status = "processing"
	StatusReadyToSend    Status = "ready_to_send"
	StatusDelivered      Status = "delivered"
	StatusIgnored        Status = "ignored"
	StatusDeliveryFailed Status = "delivery_failed"
)

type IncomingUpdate struct {
	UpdateID      int64
	UserID        *int64
	ChatID        *int64
	MessageID     *int64
	MessageAt     *time.Time
	ReferenceDate *expense.Date
	Text          *string
	Status        Status
}

type Update struct {
	IncomingUpdate
	ProcessingAttempts int
	DeliveryAttempts   int
	NextAttemptAt      *time.Time
	Result             json.RawMessage
	ReplyText          *string
	LastErrorCode      *string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type Delivery struct {
	UpdateID int64
	ChatID   int64
	Text     string
	Attempts int
}
