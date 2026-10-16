package appbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/agent"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/bot"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/expense"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/llm"
	postgresstore "github.com/Anemiaaaa/HomeBudget-AI/internal/storage/postgres"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/telegram"
)

type Provider interface {
	Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error)
}

type Store interface {
	ClaimNext(context.Context) (bot.Update, error)
	Complete(context.Context, int64, postgresstore.Completion) error
	LoadSession(context.Context, int64, int64) (postgresstore.Session, error)
	NextDelivery(context.Context) (bot.Delivery, error)
	MarkDelivered(context.Context, int64) error
	MarkDeliveryFailed(context.Context, int64, string, time.Time) error
}

type Sender interface {
	SendMessage(context.Context, int64, string) (telegram.SentMessage, error)
}

type Processor struct {
	store           Store
	provider        Provider
	reader          *expense.OwnerService
	sender          Sender
	model           string
	maxLLMCalls     int
	maxToolCalls    int
	maxContextBytes int
	now             func() time.Time
}

type ProcessorOptions struct {
	MaxLLMCalls     int
	MaxToolCalls    int
	MaxContextBytes int
}

func NewProcessor(store Store, provider Provider, reader *expense.OwnerService, sender Sender, model string, options ...ProcessorOptions) *Processor {
	processor := &Processor{
		store: store, provider: provider, reader: reader, sender: sender, model: model,
		maxLLMCalls: 4, maxToolCalls: 6, maxContextBytes: 32 << 10, now: time.Now,
	}
	if len(options) > 0 {
		if options[0].MaxLLMCalls > 0 {
			processor.maxLLMCalls = options[0].MaxLLMCalls
		}
		if options[0].MaxToolCalls > 0 {
			processor.maxToolCalls = options[0].MaxToolCalls
		}
		if options[0].MaxContextBytes > 0 {
			processor.maxContextBytes = options[0].MaxContextBytes
		}
	}
	return processor
}

func (p *Processor) ProcessOne(ctx context.Context) error {
	update, err := p.store.ClaimNext(ctx)
	if err != nil {
		return err
	}
	if update.UserID == nil || update.ChatID == nil || update.ReferenceDate == nil || update.Text == nil {
		return p.completeMessage(ctx, update.UpdateID, "unsupported_update", "Не удалось обработать это сообщение.", nil)
	}

	session, err := p.store.LoadSession(ctx, *update.UserID, *update.ChatID)
	if err != nil {
		return err
	}
	if len(session.PendingAction) > 0 && (session.ExpiresAt == nil || session.ExpiresAt.After(p.now())) {
		handled, err := p.handlePending(ctx, update, session.PendingAction)
		if err != nil || handled {
			return err
		}
	}
	return p.runAgent(ctx, update)
}

func (p *Processor) runAgent(ctx context.Context, update bot.Update) error {
	ownerID := *update.UserID
	turn := &agent.Turn{OwnerID: ownerID, ReferenceDate: *update.ReferenceDate}
	executor := agent.NewExecutor(p.reader)
	definitions := agent.Definitions()
	messages := []llm.Message{
		{Role: "system", Content: systemPrompt(update.ReferenceDate.String())},
		{Role: "user", Content: *update.Text},
	}
	toolCalls := 0
	var lastRead *agent.Result

	for llmCalls := 0; llmCalls < p.maxLLMCalls; llmCalls++ {
		if contextSize(messages, definitions) > p.maxContextBytes {
			return p.completeMessage(ctx, update.UpdateID, "context_too_large", "Запрос слишком большой. Сократите его и попробуйте ещё раз.", nil)
		}
		response, err := p.provider.Chat(ctx, llm.ChatRequest{Model: p.model, Messages: messages, Tools: definitions, MaxTokens: 800})
		if err != nil {
			return p.completeMessage(ctx, update.UpdateID, "llm_unavailable", "Сейчас не получилось обработать запрос. Ничего не изменено.", nil)
		}
		if len(response.ToolCalls) == 0 {
			if lastRead != nil {
				result, reply, err := resultAndReply(*lastRead)
				if err == nil {
					return p.completeMessage(ctx, update.UpdateID, "read_result", reply, result)
				}
			}
			return p.completeMessage(ctx, update.UpdateID, "clarification_required", "Уточните, что нужно сделать с расходами.", nil)
		}
		toolCalls += len(response.ToolCalls)
		if toolCalls > p.maxToolCalls {
			return p.completeMessage(ctx, update.UpdateID, "tool_limit", "Запрос слишком сложный. Разделите его на несколько сообщений.", nil)
		}
		terminalCalls := terminalCount(response.ToolCalls)
		if terminalCalls > 1 || (terminalCalls == 1 && len(response.ToolCalls) > 1) {
			return p.completeMessage(ctx, update.UpdateID, "multiple_actions", "В одном сообщении можно изменить только один расход. Разделите запрос.", nil)
		}

		messages = append(messages, llm.Message{Role: "assistant", ToolCalls: response.ToolCalls})
		calledTerminal := false
		for _, call := range response.ToolCalls {
			if isTerminal(call.Name) {
				calledTerminal = true
				return p.completeTool(ctx, update, turn, call)
			}
			result, err := executor.Execute(ctx, turn, agent.Call{Name: call.Name, Arguments: call.Arguments})
			if err != nil {
				messages = append(messages, llm.Message{Role: "tool", ToolCallID: call.ID, Content: toolError(err)})
				continue
			}
			payload, err := json.Marshal(result.Payload)
			if err != nil {
				return err
			}
			copy := result
			lastRead = &copy
			messages = append(messages, llm.Message{Role: "tool", ToolCallID: call.ID, Content: string(payload)})
		}
		if calledTerminal {
			return nil
		}
	}
	return p.completeMessage(ctx, update.UpdateID, "llm_limit", "Не удалось однозначно выполнить запрос. Сформулируйте его короче.", nil)
}

func (p *Processor) completeTool(ctx context.Context, update bot.Update, turn *agent.Turn, call llm.ToolCall) error {
	err := p.store.Complete(ctx, update.UpdateID, func(ctx context.Context, unit *postgresstore.CompletionUnit) (json.RawMessage, string, error) {
		transactional := agent.NewExecutor(unit.Expenses)
		result, err := transactional.Execute(ctx, turn, agent.Call{Name: call.Name, Arguments: call.Arguments})
		if err != nil {
			return nil, "", err
		}
		if err := unit.ClearSession(ctx); err != nil {
			return nil, "", err
		}
		payload, reply, err := resultAndReply(result)
		if err != nil {
			return nil, "", err
		}
		if call.Name == "delete_expense" {
			pending, err := pendingDelete(payload)
			if err != nil {
				return nil, "", err
			}
			if err := unit.SaveSession(ctx, pending, p.now().Add(24*time.Hour)); err != nil {
				return nil, "", err
			}
		}
		if call.Name == "ask_clarification" {
			if err := unit.SaveSession(ctx, payload, p.now().Add(time.Hour)); err != nil {
				return nil, "", err
			}
		}
		return payload, reply, nil
	})
	if err == nil {
		return nil
	}
	if isSafeToolFailure(err) {
		return p.completeMessage(ctx, update.UpdateID, "tool_failed", safeToolReply(err), nil)
	}
	return err
}

type pendingAction struct {
	Kind            string `json:"kind"`
	ExpenseID       string `json:"expense_id"`
	ExpectedVersion string `json:"expected_version"`
	Prompt          string `json:"prompt"`
}

func (p *Processor) handlePending(ctx context.Context, update bot.Update, raw json.RawMessage) (bool, error) {
	var pending pendingAction
	if err := json.Unmarshal(raw, &pending); err != nil || pending.Kind != "confirm_delete" {
		return false, nil
	}
	answer := normalizeAnswer(*update.Text)
	if isCancel(answer) {
		return true, p.store.Complete(ctx, update.UpdateID, func(ctx context.Context, unit *postgresstore.CompletionUnit) (json.RawMessage, string, error) {
			if err := unit.ClearSession(ctx); err != nil {
				return nil, "", err
			}
			return json.RawMessage(`{"status":"cancelled"}`), "Удаление отменено.", nil
		})
	}
	if !isConfirm(answer) {
		return true, p.completeMessage(ctx, update.UpdateID, "confirmation_required", pending.Prompt+" Ответьте «да, удали» или «отмена».", nil)
	}
	id, err := strconv.ParseInt(pending.ExpenseID, 10, 64)
	if err != nil {
		return true, err
	}
	version, err := strconv.ParseInt(pending.ExpectedVersion, 10, 64)
	if err != nil {
		return true, err
	}
	err = p.store.Complete(ctx, update.UpdateID, func(ctx context.Context, unit *postgresstore.CompletionUnit) (json.RawMessage, string, error) {
		if err := unit.Expenses.Delete(ctx, unit.UserID, id, version); err != nil {
			return nil, "", err
		}
		if err := unit.ClearSession(ctx); err != nil {
			return nil, "", err
		}
		result, _ := json.Marshal(map[string]string{"status": "deleted", "expense_id": pending.ExpenseID})
		return result, "Расход №" + pending.ExpenseID + " удалён.", nil
	})
	if err == nil {
		return true, nil
	}
	if errors.Is(err, expense.ErrNotFound) || errors.Is(err, expense.ErrVersionConflict) {
		reply := "Расход уже изменился или удалён. Выберите его заново."
		return true, p.store.Complete(ctx, update.UpdateID, func(ctx context.Context, unit *postgresstore.CompletionUnit) (json.RawMessage, string, error) {
			if clearErr := unit.ClearSession(ctx); clearErr != nil {
				return nil, "", clearErr
			}
			return json.RawMessage(`{"status":"stale_confirmation"}`), reply, nil
		})
	}
	return true, err
}

func (p *Processor) completeMessage(ctx context.Context, updateID int64, code, reply string, result json.RawMessage) error {
	if result == nil {
		result, _ = json.Marshal(map[string]string{"status": code})
	}
	return p.store.Complete(ctx, updateID, func(context.Context, *postgresstore.CompletionUnit) (json.RawMessage, string, error) {
		return result, reply, nil
	})
}

func (p *Processor) DeliverOne(ctx context.Context) error {
	delivery, err := p.store.NextDelivery(ctx)
	if err != nil {
		return err
	}
	if _, err := p.sender.SendMessage(ctx, delivery.ChatID, delivery.Text); err != nil {
		delay := 5 * time.Second * time.Duration(1<<min(delivery.Attempts, 6))
		return p.store.MarkDeliveryFailed(ctx, delivery.UpdateID, "telegram_send_failed", p.now().Add(delay))
	}
	return p.store.MarkDelivered(ctx, delivery.UpdateID)
}

func terminalCount(calls []llm.ToolCall) int {
	count := 0
	for _, call := range calls {
		if isTerminal(call.Name) {
			count++
		}
	}
	return count
}

func isTerminal(name string) bool {
	return name == "create_expense" || name == "update_expense" || name == "delete_expense" || name == "ask_clarification"
}

func contextSize(messages []llm.Message, definitions []agent.Definition) int {
	encoded, _ := json.Marshal(struct {
		Messages    []llm.Message      `json:"messages"`
		Definitions []agent.Definition `json:"tools"`
	}{messages, definitions})
	return len(encoded)
}

func systemPrompt(referenceDate string) string {
	return `Ты управляешь личными расходами через tools. Текущая дата: ` + referenceDate + `.
Всегда читай запись перед изменением или удалением. Не передавай user_id: владелец задан сервером.
За один ход разрешена одна мутация. Для неполного или неоднозначного запроса используй ask_clarification.
delete_expense только готовит подтверждение. Не утверждай, что данные изменены, без успешного tool result.
Категории: food, transport, housing, entertainment, health, shopping, other.`
}

func toolError(err error) string {
	code := "tool_failed"
	switch {
	case errors.Is(err, agent.ErrInvalidArguments):
		code = "invalid_arguments"
	case errors.Is(err, agent.ErrReadRequired):
		code = "read_required"
	case errors.Is(err, expense.ErrNotFound):
		code = "not_found"
	case errors.Is(err, expense.ErrVersionConflict):
		code = "version_conflict"
	}
	encoded, _ := json.Marshal(map[string]string{"error": code})
	return string(encoded)
}

func isSafeToolFailure(err error) bool {
	var validationError *expense.ValidationError
	return errors.As(err, &validationError) || errors.Is(err, agent.ErrInvalidArguments) ||
		errors.Is(err, agent.ErrReadRequired) || errors.Is(err, agent.ErrOldAmountMismatch) ||
		errors.Is(err, expense.ErrNotFound) || errors.Is(err, expense.ErrVersionConflict)
}

func safeToolReply(err error) string {
	switch {
	case errors.Is(err, expense.ErrNotFound):
		return "Расход не найден. Возможно, он уже удалён или принадлежит другому пользователю."
	case errors.Is(err, expense.ErrVersionConflict):
		return "Расход уже изменился. Повторите запрос, чтобы получить актуальные данные."
	case errors.Is(err, agent.ErrOldAmountMismatch):
		return "Указанная старая сумма не совпадает с записью. Уточните расход."
	default:
		return "Не удалось выполнить действие: проверьте сумму, дату и категорию."
	}
}

func normalizeAnswer(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	fields := strings.FieldsFunc(value, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSpace(r) })
	return strings.Join(fields, " ")
}

func isConfirm(value string) bool {
	return value == "да" || value == "да удали" || value == "подтверждаю"
}

func isCancel(value string) bool {
	return value == "нет" || value == "отмена" || value == "не удаляй"
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func resultAndReply(result agent.Result) (json.RawMessage, string, error) {
	payload, err := json.Marshal(result.Payload)
	if err != nil {
		return nil, "", err
	}
	switch result.Name {
	case "create_expense", "update_expense":
		var item expenseView
		if err := json.Unmarshal(payload, &item); err != nil {
			return nil, "", err
		}
		verb := "Добавлено"
		if result.Name == "update_expense" {
			verb = "Обновлено"
		}
		return payload, fmt.Sprintf("%s: %s — %s, %s, %s. №%s", verb, title(item), rubles(item.Amount), categoryLabel(item.Category), displayDate(item.SpentAt), item.ID), nil
	case "delete_expense":
		var prepared struct {
			Expense expenseView `json:"expense"`
		}
		if err := json.Unmarshal(payload, &prepared); err != nil {
			return nil, "", err
		}
		return payload, fmt.Sprintf("Удалить %s — %s от %s, №%s?", title(prepared.Expense), rubles(prepared.Expense.Amount), displayDate(prepared.Expense.SpentAt), prepared.Expense.ID), nil
	case "ask_clarification":
		var clarification struct {
			Question string `json:"question"`
		}
		if err := json.Unmarshal(payload, &clarification); err != nil {
			return nil, "", err
		}
		return payload, clarification.Question, nil
	case "summarize_expenses":
		var summary struct {
			TotalAmount string `json:"total_amount"`
			Count       int64  `json:"count"`
		}
		if err := json.Unmarshal(payload, &summary); err != nil {
			return nil, "", err
		}
		return payload, fmt.Sprintf("Итого: %s, операций: %d.", rubles(summary.TotalAmount), summary.Count), nil
	case "list_expenses":
		var list struct {
			Items []expenseView `json:"items"`
		}
		if err := json.Unmarshal(payload, &list); err != nil {
			return nil, "", err
		}
		if len(list.Items) == 0 {
			return payload, "Расходов не найдено.", nil
		}
		lines := make([]string, 0, len(list.Items)+1)
		lines = append(lines, "Расходы:")
		for _, item := range list.Items {
			lines = append(lines, fmt.Sprintf("• %s — %s, %s, №%s", displayDate(item.SpentAt), rubles(item.Amount), title(item), item.ID))
		}
		return payload, strings.Join(lines, "\n"), nil
	default:
		return nil, "", errors.New("unsupported tool result")
	}
}

type expenseView struct {
	ID       string           `json:"id"`
	Amount   string           `json:"amount"`
	Category expense.Category `json:"category"`
	SpentAt  string           `json:"spent_at"`
	Comment  *string          `json:"comment"`
	Version  string           `json:"version"`
}

func pendingDelete(payload json.RawMessage) (json.RawMessage, error) {
	var prepared struct {
		Expense expenseView `json:"expense"`
	}
	if err := json.Unmarshal(payload, &prepared); err != nil {
		return nil, err
	}
	prompt := fmt.Sprintf("Удалить %s — %s от %s, №%s?", title(prepared.Expense), rubles(prepared.Expense.Amount), displayDate(prepared.Expense.SpentAt), prepared.Expense.ID)
	return json.Marshal(pendingAction{Kind: "confirm_delete", ExpenseID: prepared.Expense.ID, ExpectedVersion: prepared.Expense.Version, Prompt: prompt})
}

func title(item expenseView) string {
	if item.Comment != nil && strings.TrimSpace(*item.Comment) != "" {
		return strings.TrimSpace(*item.Comment)
	}
	return categoryLabel(item.Category)
}

func categoryLabel(category expense.Category) string {
	labels := map[expense.Category]string{
		expense.CategoryFood: "еда", expense.CategoryTransport: "транспорт", expense.CategoryHousing: "жильё",
		expense.CategoryEntertainment: "развлечения", expense.CategoryHealth: "здоровье",
		expense.CategoryShopping: "покупки", expense.CategoryOther: "другое",
	}
	return labels[category]
}

func rubles(amount string) string { return strings.Replace(amount, ".", ",", 1) + " ₽" }

func displayDate(value string) string {
	date, err := time.Parse("2006-01-02", value)
	if err != nil {
		return value
	}
	return date.Format("02.01.2006")
}
