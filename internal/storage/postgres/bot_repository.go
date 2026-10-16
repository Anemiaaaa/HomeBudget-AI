package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/bot"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/expense"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNoBotWork         = errors.New("no bot work available")
	ErrInvalidBotState   = errors.New("invalid bot update state")
	ErrBotAlreadyRunning = errors.New("another bot worker already holds the singleton lock")
)

const botLockKey int64 = 0x484f4d4542554447

const botUpdateColumns = `update_id, user_id, chat_id, message_id, message_at, reference_date,
       text, status, processing_attempts, delivery_attempts, next_attempt_at, result,
       reply_text, last_error_code, created_at, updated_at`

type BotRepository struct{ pool *pgxpool.Pool }

func NewBotRepository(pool *pgxpool.Pool) *BotRepository { return &BotRepository{pool: pool} }

type BotLock struct{ conn *pgxpool.Conn }

func (r *BotRepository) AcquireLock(ctx context.Context) (*BotLock, error) {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire bot lock connection: %w", err)
	}
	var acquired bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", botLockKey).Scan(&acquired); err != nil {
		conn.Release()
		return nil, fmt.Errorf("acquire bot singleton lock: %w", err)
	}
	if !acquired {
		conn.Release()
		return nil, ErrBotAlreadyRunning
	}
	return &BotLock{conn: conn}, nil
}

func (l *BotLock) Ping(ctx context.Context) error {
	if l == nil || l.conn == nil {
		return errors.New("bot lock is not held")
	}
	return l.conn.Ping(ctx)
}

func (l *BotLock) Release(ctx context.Context) {
	if l == nil || l.conn == nil {
		return
	}
	_, _ = l.conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", botLockKey)
	l.conn.Release()
	l.conn = nil
}

func (r *BotRepository) NextOffset(ctx context.Context) (int64, error) {
	var offset int64
	if err := r.pool.QueryRow(ctx, "SELECT next_offset FROM bot_polling_state WHERE singleton = TRUE").Scan(&offset); err != nil {
		return 0, fmt.Errorf("read bot polling offset: %w", err)
	}
	return offset, nil
}

func (r *BotRepository) Ingest(ctx context.Context, updates []bot.IncomingUpdate, nextOffset int64) (int64, error) {
	if nextOffset < 0 {
		return 0, errors.New("next bot offset must not be negative")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin bot update ingestion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var inserted int64
	for _, update := range updates {
		if err := validateIncoming(update); err != nil {
			return 0, err
		}
		var referenceDate any
		if update.ReferenceDate != nil {
			referenceDate = update.ReferenceDate.String()
		}
		tag, err := tx.Exec(ctx, `INSERT INTO bot_updates
			(update_id, user_id, chat_id, message_id, message_at, reference_date, text, status)
			VALUES ($1, $2, $3, $4, $5, $6::date, $7, $8)
			ON CONFLICT (update_id) DO NOTHING`,
			update.UpdateID, update.UserID, update.ChatID, update.MessageID, update.MessageAt,
			referenceDate, update.Text, update.Status)
		if err != nil {
			return 0, fmt.Errorf("insert bot update %d: %w", update.UpdateID, err)
		}
		inserted += tag.RowsAffected()
	}
	if _, err := tx.Exec(ctx, `UPDATE bot_polling_state
		SET next_offset = greatest(next_offset, $1), updated_at = now() WHERE singleton = TRUE`, nextOffset); err != nil {
		return 0, fmt.Errorf("advance bot polling offset: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit bot update ingestion: %w", err)
	}
	return inserted, nil
}

func (r *BotRepository) RecoverProcessing(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE bot_updates SET status = 'pending', updated_at = now()
		WHERE status = 'processing'`)
	if err != nil {
		return 0, fmt.Errorf("recover processing bot updates: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (r *BotRepository) ClaimNext(ctx context.Context) (bot.Update, error) {
	row := r.pool.QueryRow(ctx, `WITH candidate AS (
		SELECT update_id FROM bot_updates
		WHERE status = 'pending' AND (next_attempt_at IS NULL OR next_attempt_at <= now())
		ORDER BY update_id
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	)
	UPDATE bot_updates AS item
	SET status = 'processing', processing_attempts = processing_attempts + 1, updated_at = now()
	FROM candidate
	WHERE item.update_id = candidate.update_id
	RETURNING `+prefixedColumns("item"))
	claimed, err := scanBotUpdate(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return bot.Update{}, ErrNoBotWork
	}
	if err != nil {
		return bot.Update{}, fmt.Errorf("claim bot update: %w", err)
	}
	return claimed, nil
}

type CompletionUnit struct {
	Expenses *expense.OwnerService
	tx       pgx.Tx
	UserID   int64
	ChatID   int64
}

func (u *CompletionUnit) SaveSession(ctx context.Context, pending json.RawMessage, expiresAt time.Time) error {
	if !json.Valid(pending) {
		return errors.New("invalid bot pending action")
	}
	_, err := u.tx.Exec(ctx, `INSERT INTO bot_sessions (user_id, chat_id, pending_action, expires_at)
		VALUES ($1, $2, $3::jsonb, $4)
		ON CONFLICT (user_id, chat_id) DO UPDATE SET
			pending_action = excluded.pending_action, expires_at = excluded.expires_at, updated_at = now()`,
		u.UserID, u.ChatID, []byte(pending), expiresAt)
	if err != nil {
		return fmt.Errorf("save bot session in completion: %w", err)
	}
	return nil
}

func (u *CompletionUnit) ClearSession(ctx context.Context) error {
	if _, err := u.tx.Exec(ctx, "DELETE FROM bot_sessions WHERE user_id = $1 AND chat_id = $2", u.UserID, u.ChatID); err != nil {
		return fmt.Errorf("clear bot session in completion: %w", err)
	}
	return nil
}

type Completion func(context.Context, *CompletionUnit) (json.RawMessage, string, error)

func (r *BotRepository) Complete(ctx context.Context, updateID int64, complete Completion) error {
	if updateID <= 0 || complete == nil {
		return errors.New("bot completion requires update ID and callback")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin bot completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status bot.Status
	var userID, chatID pgtype.Int8
	if err := tx.QueryRow(ctx, "SELECT status, user_id, chat_id FROM bot_updates WHERE update_id = $1 FOR UPDATE", updateID).Scan(&status, &userID, &chatID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return expense.ErrNotFound
		}
		return fmt.Errorf("lock bot update: %w", err)
	}
	if status != bot.StatusProcessing {
		return fmt.Errorf("%w: cannot complete %s update", ErrInvalidBotState, status)
	}
	if !userID.Valid || !chatID.Valid {
		return errors.New("processing bot update is missing actor")
	}

	unit := &CompletionUnit{
		Expenses: expense.NewOwnerService(NewExpenseRepositoryTx(tx)), tx: tx,
		UserID: userID.Int64, ChatID: chatID.Int64,
	}
	result, reply, err := complete(ctx, unit)
	if err != nil {
		return err
	}
	reply = strings.TrimSpace(reply)
	if !json.Valid(result) || reply == "" {
		return errors.New("bot completion must contain valid result JSON and reply text")
	}
	if _, err := tx.Exec(ctx, `UPDATE bot_updates
		SET status = 'ready_to_send', result = $2::jsonb, reply_text = $3,
			next_attempt_at = NULL, last_error_code = NULL, updated_at = now()
		WHERE update_id = $1`, updateID, []byte(result), reply); err != nil {
		return fmt.Errorf("save bot completion: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit bot completion: %w", err)
	}
	return nil
}

func (r *BotRepository) NextDelivery(ctx context.Context) (bot.Delivery, error) {
	var delivery bot.Delivery
	err := r.pool.QueryRow(ctx, `SELECT update_id, chat_id, reply_text, delivery_attempts
		FROM bot_updates
		WHERE status IN ('ready_to_send', 'delivery_failed')
		  AND (next_attempt_at IS NULL OR next_attempt_at <= now())
		  AND chat_id IS NOT NULL AND reply_text IS NOT NULL
		ORDER BY update_id LIMIT 1`).Scan(&delivery.UpdateID, &delivery.ChatID, &delivery.Text, &delivery.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return bot.Delivery{}, ErrNoBotWork
	}
	if err != nil {
		return bot.Delivery{}, fmt.Errorf("select bot delivery: %w", err)
	}
	return delivery, nil
}

func (r *BotRepository) MarkDelivered(ctx context.Context, updateID int64) error {
	tag, err := r.pool.Exec(ctx, `UPDATE bot_updates
		SET status = 'delivered', delivery_attempts = delivery_attempts + 1,
			next_attempt_at = NULL, last_error_code = NULL, updated_at = now()
		WHERE update_id = $1 AND status IN ('ready_to_send', 'delivery_failed')`, updateID)
	if err != nil {
		return fmt.Errorf("mark bot reply delivered: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrInvalidBotState
	}
	return nil
}

func (r *BotRepository) MarkDeliveryFailed(ctx context.Context, updateID int64, errorCode string, retryAt time.Time) error {
	errorCode = strings.TrimSpace(errorCode)
	if errorCode == "" {
		errorCode = "telegram_send_failed"
	}
	tag, err := r.pool.Exec(ctx, `UPDATE bot_updates
		SET status = 'delivery_failed', delivery_attempts = delivery_attempts + 1,
			next_attempt_at = $2, last_error_code = $3, updated_at = now()
		WHERE update_id = $1 AND status IN ('ready_to_send', 'delivery_failed')`, updateID, retryAt, errorCode)
	if err != nil {
		return fmt.Errorf("save bot delivery failure: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrInvalidBotState
	}
	return nil
}

func (r *BotRepository) Get(ctx context.Context, updateID int64) (bot.Update, error) {
	result, err := scanBotUpdate(r.pool.QueryRow(ctx, "SELECT "+botUpdateColumns+" FROM bot_updates WHERE update_id = $1", updateID))
	if errors.Is(err, pgx.ErrNoRows) {
		return bot.Update{}, expense.ErrNotFound
	}
	if err != nil {
		return bot.Update{}, fmt.Errorf("get bot update: %w", err)
	}
	return result, nil
}

type Session struct {
	PendingAction json.RawMessage
	ExpiresAt     *time.Time
}

func (r *BotRepository) LoadSession(ctx context.Context, userID, chatID int64) (Session, error) {
	var pending []byte
	var expires pgtype.Timestamptz
	err := r.pool.QueryRow(ctx, `SELECT pending_action, expires_at FROM bot_sessions
		WHERE user_id = $1 AND chat_id = $2`, userID, chatID).Scan(&pending, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, nil
	}
	if err != nil {
		return Session{}, fmt.Errorf("load bot session: %w", err)
	}
	result := Session{PendingAction: json.RawMessage(pending)}
	if expires.Valid {
		result.ExpiresAt = &expires.Time
	}
	return result, nil
}

func (r *BotRepository) SaveSession(ctx context.Context, userID, chatID int64, pending json.RawMessage, expiresAt time.Time) error {
	if userID <= 0 || chatID == 0 || !json.Valid(pending) {
		return errors.New("invalid bot session")
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO bot_sessions (user_id, chat_id, pending_action, expires_at)
		VALUES ($1, $2, $3::jsonb, $4)
		ON CONFLICT (user_id, chat_id) DO UPDATE SET
			pending_action = excluded.pending_action, expires_at = excluded.expires_at, updated_at = now()`,
		userID, chatID, []byte(pending), expiresAt)
	if err != nil {
		return fmt.Errorf("save bot session: %w", err)
	}
	return nil
}

func (r *BotRepository) ClearSession(ctx context.Context, userID, chatID int64) error {
	if _, err := r.pool.Exec(ctx, "DELETE FROM bot_sessions WHERE user_id = $1 AND chat_id = $2", userID, chatID); err != nil {
		return fmt.Errorf("clear bot session: %w", err)
	}
	return nil
}

func validateIncoming(update bot.IncomingUpdate) error {
	if update.UpdateID < 0 {
		return errors.New("bot update ID must not be negative")
	}
	if update.Status != bot.StatusPending && update.Status != bot.StatusIgnored {
		return errors.New("incoming bot update must be pending or ignored")
	}
	if update.Status == bot.StatusPending && (update.UserID == nil || update.ChatID == nil || update.MessageID == nil || update.MessageAt == nil || update.ReferenceDate == nil || update.Text == nil) {
		return errors.New("pending bot update is missing message fields")
	}
	return nil
}

func prefixedColumns(alias string) string {
	columns := strings.Split(botUpdateColumns, ",")
	for index, column := range columns {
		columns[index] = alias + "." + strings.TrimSpace(column)
	}
	return strings.Join(columns, ", ")
}

func scanBotUpdate(row scanner) (bot.Update, error) {
	var result bot.Update
	var userID, chatID, messageID pgtype.Int8
	var messageAt, nextAttemptAt pgtype.Timestamptz
	var referenceDate pgtype.Date
	var text, replyText, lastErrorCode pgtype.Text
	var storedResult []byte
	err := row.Scan(
		&result.UpdateID, &userID, &chatID, &messageID, &messageAt, &referenceDate,
		&text, &result.Status, &result.ProcessingAttempts, &result.DeliveryAttempts,
		&nextAttemptAt, &storedResult, &replyText, &lastErrorCode, &result.CreatedAt, &result.UpdatedAt,
	)
	if err != nil {
		return bot.Update{}, err
	}
	if userID.Valid {
		result.UserID = &userID.Int64
	}
	if chatID.Valid {
		result.ChatID = &chatID.Int64
	}
	if messageID.Valid {
		result.MessageID = &messageID.Int64
	}
	if messageAt.Valid {
		result.MessageAt = &messageAt.Time
	}
	if referenceDate.Valid {
		date, err := expense.ParseDate(referenceDate.Time.Format("2006-01-02"))
		if err != nil {
			return bot.Update{}, err
		}
		result.ReferenceDate = &date
	}
	if text.Valid {
		result.Text = &text.String
	}
	if nextAttemptAt.Valid {
		result.NextAttemptAt = &nextAttemptAt.Time
	}
	if storedResult != nil {
		result.Result = json.RawMessage(storedResult)
	}
	if replyText.Valid {
		result.ReplyText = &replyText.String
	}
	if lastErrorCode.Valid {
		result.LastErrorCode = &lastErrorCode.String
	}
	return result, nil
}
