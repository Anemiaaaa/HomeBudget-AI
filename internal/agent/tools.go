package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/expense"
)

const maxToolArguments = 16 << 10

var (
	ErrUnknownTool       = errors.New("unknown tool")
	ErrInvalidArguments  = errors.New("invalid tool arguments")
	ErrReadRequired      = errors.New("expense must be read in the current turn")
	ErrMutationLimit     = errors.New("turn already contains a mutation or pending action")
	ErrOldAmountMismatch = errors.New("expense amount differs from expected_old_amount")
)

type OwnerExpenses interface {
	Create(context.Context, int64, expense.CreateFields, expense.Date) (expense.Expense, error)
	Get(context.Context, int64, int64) (expense.Expense, error)
	List(context.Context, int64, expense.ListFilter) (expense.ListResult, error)
	Summary(context.Context, int64, expense.ListFilter) (expense.Summary, error)
	Update(context.Context, int64, int64, expense.UpdateInput, expense.Date) (expense.Expense, error)
}

type Executor struct{ expenses OwnerExpenses }

func NewExecutor(expenses OwnerExpenses) *Executor { return &Executor{expenses: expenses} }

type Turn struct {
	OwnerID       int64
	ReferenceDate expense.Date
	Reads         map[int64]ReadSnapshot
	Finished      bool
}

type ReadSnapshot struct {
	ID         int64            `json:"id"`
	Version    int64            `json:"version"`
	Amount     expense.Money    `json:"-"`
	AmountText string           `json:"amount"`
	Category   expense.Category `json:"category"`
	SpentAt    string           `json:"spent_at"`
	Comment    *string          `json:"comment"`
}

type Call struct {
	Name      string
	Arguments json.RawMessage
}

type Result struct {
	Name      string
	Payload   any
	Mutated   bool
	StopsTurn bool
}

func (e *Executor) Execute(ctx context.Context, turn *Turn, call Call) (Result, error) {
	if turn == nil || turn.OwnerID <= 0 {
		return Result{}, fmt.Errorf("%w: trusted owner is missing", ErrInvalidArguments)
	}
	if turn.Finished {
		return Result{}, ErrMutationLimit
	}
	if turn.Reads == nil {
		turn.Reads = make(map[int64]ReadSnapshot)
	}

	switch call.Name {
	case "create_expense":
		return e.create(ctx, turn, call.Arguments)
	case "list_expenses":
		return e.list(ctx, turn, call.Arguments)
	case "summarize_expenses":
		return e.summary(ctx, turn, call.Arguments)
	case "update_expense":
		return e.update(ctx, turn, call.Arguments)
	case "delete_expense":
		return e.prepareDelete(ctx, turn, call.Arguments)
	case "ask_clarification":
		return e.askClarification(turn, call.Arguments)
	default:
		return Result{}, fmt.Errorf("%w: %s", ErrUnknownTool, call.Name)
	}
}

type createArgs struct {
	Amount   string           `json:"amount"`
	Category expense.Category `json:"category"`
	SpentAt  string           `json:"spent_at"`
	Comment  *string          `json:"comment,omitempty"`
}

func (e *Executor) create(ctx context.Context, turn *Turn, raw json.RawMessage) (Result, error) {
	var args createArgs
	if err := decodeArguments(raw, &args); err != nil {
		return Result{}, err
	}
	created, err := e.expenses.Create(ctx, turn.OwnerID, expense.CreateFields{
		Amount: args.Amount, Category: args.Category, SpentAt: args.SpentAt, Comment: args.Comment,
	}, turn.ReferenceDate)
	if err != nil {
		return Result{}, err
	}
	turn.Finished = true
	return Result{Name: "create_expense", Payload: toExpense(created), Mutated: true, StopsTurn: true}, nil
}

type listArgs struct {
	ExpenseID string            `json:"expense_id,omitempty"`
	Category  *expense.Category `json:"category,omitempty"`
	DateFrom  string            `json:"date_from,omitempty"`
	DateTo    string            `json:"date_to,omitempty"`
	Query     string            `json:"query,omitempty"`
	Order     expense.ListOrder `json:"order,omitempty"`
	Limit     int               `json:"limit,omitempty"`
	Offset    int               `json:"offset,omitempty"`
}

type listPayload struct {
	Items      []expensePayload `json:"items"`
	TotalCount int64            `json:"total_count"`
	HasMore    bool             `json:"has_more"`
}

func (e *Executor) list(ctx context.Context, turn *Turn, raw json.RawMessage) (Result, error) {
	var args listArgs
	if err := decodeArguments(raw, &args); err != nil {
		return Result{}, err
	}
	if args.ExpenseID != "" {
		id, err := positiveID(args.ExpenseID)
		if err != nil {
			return Result{}, err
		}
		item, err := e.expenses.Get(ctx, turn.OwnerID, id)
		if err != nil {
			return Result{}, err
		}
		remember(turn, item)
		return Result{Name: "list_expenses", Payload: listPayload{Items: []expensePayload{toExpense(item)}, TotalCount: 1}}, nil
	}
	if args.Limit == 0 {
		args.Limit = 10
	}
	if args.Limit < 1 || args.Limit > 20 {
		return Result{}, invalid("limit must be between 1 and 20")
	}
	filter, err := filterFrom(args.Category, args.DateFrom, args.DateTo, args.Query)
	if err != nil {
		return Result{}, err
	}
	filter.Order, filter.Limit, filter.Offset = args.Order, args.Limit, args.Offset
	result, err := e.expenses.List(ctx, turn.OwnerID, filter)
	if err != nil {
		return Result{}, err
	}
	items := make([]expensePayload, 0, len(result.Items))
	for _, item := range result.Items {
		remember(turn, item)
		items = append(items, toExpense(item))
	}
	return Result{Name: "list_expenses", Payload: listPayload{
		Items: items, TotalCount: result.TotalCount, HasMore: int64(result.Offset+len(result.Items)) < result.TotalCount,
	}}, nil
}

type summaryArgs struct {
	Category *expense.Category `json:"category,omitempty"`
	DateFrom string            `json:"date_from,omitempty"`
	DateTo   string            `json:"date_to,omitempty"`
	Query    string            `json:"query,omitempty"`
}

type summaryPayload struct {
	TotalAmount string                   `json:"total_amount"`
	Count       int64                    `json:"count"`
	ByCategory  []categorySummaryPayload `json:"by_category"`
}

type categorySummaryPayload struct {
	Category expense.Category `json:"category"`
	Amount   string           `json:"amount"`
	Count    int64            `json:"count"`
}

func (e *Executor) summary(ctx context.Context, turn *Turn, raw json.RawMessage) (Result, error) {
	var args summaryArgs
	if err := decodeArguments(raw, &args); err != nil {
		return Result{}, err
	}
	filter, err := filterFrom(args.Category, args.DateFrom, args.DateTo, args.Query)
	if err != nil {
		return Result{}, err
	}
	result, err := e.expenses.Summary(ctx, turn.OwnerID, filter)
	if err != nil {
		return Result{}, err
	}
	byCategory := make([]categorySummaryPayload, 0, len(result.ByCategory))
	for _, item := range result.ByCategory {
		byCategory = append(byCategory, categorySummaryPayload{Category: item.Category, Amount: item.Amount.String(), Count: item.Count})
	}
	return Result{Name: "summarize_expenses", Payload: summaryPayload{
		TotalAmount: result.TotalAmount.String(), Count: result.Count, ByCategory: byCategory,
	}}, nil
}

type optionalJSON[T any] struct {
	Set   bool
	Value T
}

func (o *optionalJSON[T]) UnmarshalJSON(data []byte) error {
	o.Set = true
	return json.Unmarshal(data, &o.Value)
}

type updatePatch struct {
	Amount   optionalJSON[string]           `json:"amount"`
	Category optionalJSON[expense.Category] `json:"category"`
	SpentAt  optionalJSON[string]           `json:"spent_at"`
	Comment  optionalJSON[*string]          `json:"comment"`
}

type updateArgs struct {
	ExpenseID         string      `json:"expense_id"`
	ExpectedVersion   string      `json:"expected_version"`
	Patch             updatePatch `json:"patch"`
	ExpectedOldAmount string      `json:"expected_old_amount,omitempty"`
}

func (e *Executor) update(ctx context.Context, turn *Turn, raw json.RawMessage) (Result, error) {
	var args updateArgs
	if err := decodeArguments(raw, &args); err != nil {
		return Result{}, err
	}
	id, version, err := target(args.ExpenseID, args.ExpectedVersion, turn)
	if err != nil {
		return Result{}, err
	}
	if args.ExpectedOldAmount != "" {
		expected, err := expense.ParseMoney(args.ExpectedOldAmount)
		if err != nil {
			return Result{}, invalid("expected_old_amount is invalid")
		}
		if expected.Kopecks() != turn.Reads[id].Amount.Kopecks() {
			return Result{}, ErrOldAmountMismatch
		}
	}
	input := expense.UpdateInput{
		ExpectedVersion: version,
		Amount:          expense.Optional[string]{Set: args.Patch.Amount.Set, Value: args.Patch.Amount.Value},
		Category:        expense.Optional[expense.Category]{Set: args.Patch.Category.Set, Value: args.Patch.Category.Value},
		SpentAt:         expense.Optional[string]{Set: args.Patch.SpentAt.Set, Value: args.Patch.SpentAt.Value},
		Comment:         expense.Optional[*string]{Set: args.Patch.Comment.Set, Value: args.Patch.Comment.Value},
	}
	updated, err := e.expenses.Update(ctx, turn.OwnerID, id, input, turn.ReferenceDate)
	if err != nil {
		return Result{}, err
	}
	turn.Finished = true
	return Result{Name: "update_expense", Payload: toExpense(updated), Mutated: true, StopsTurn: true}, nil
}

type deleteArgs struct {
	ExpenseID       string `json:"expense_id"`
	ExpectedVersion string `json:"expected_version"`
}

type deletePayload struct {
	Expense              expensePayload `json:"expense"`
	ConfirmationRequired bool           `json:"confirmation_required"`
}

func (e *Executor) prepareDelete(ctx context.Context, turn *Turn, raw json.RawMessage) (Result, error) {
	var args deleteArgs
	if err := decodeArguments(raw, &args); err != nil {
		return Result{}, err
	}
	id, _, err := target(args.ExpenseID, args.ExpectedVersion, turn)
	if err != nil {
		return Result{}, err
	}
	item, err := e.expenses.Get(ctx, turn.OwnerID, id)
	if err != nil {
		return Result{}, err
	}
	snapshot := turn.Reads[id]
	if item.Version != snapshot.Version {
		return Result{}, expense.ErrVersionConflict
	}
	turn.Finished = true
	return Result{Name: "delete_expense", Payload: deletePayload{Expense: toExpense(item), ConfirmationRequired: true}, StopsTurn: true}, nil
}

type clarificationArgs struct {
	Kind         string          `json:"kind"`
	Question     string          `json:"question"`
	Draft        json.RawMessage `json:"draft,omitempty"`
	CandidateIDs []string        `json:"candidate_ids,omitempty"`
}

type clarificationPayload struct {
	Kind       string          `json:"kind"`
	Question   string          `json:"question"`
	Candidates []ReadSnapshot  `json:"candidates,omitempty"`
	Draft      json.RawMessage `json:"draft,omitempty"`
}

func (e *Executor) askClarification(turn *Turn, raw json.RawMessage) (Result, error) {
	var args clarificationArgs
	if err := decodeArguments(raw, &args); err != nil {
		return Result{}, err
	}
	args.Question = strings.TrimSpace(args.Question)
	if args.Kind != "clarify_create" && args.Kind != "clarify_request" && args.Kind != "choose_target" {
		return Result{}, invalid("unsupported clarification kind")
	}
	if args.Question == "" || len([]rune(args.Question)) > 500 {
		return Result{}, invalid("question must contain 1 to 500 characters")
	}
	candidates := make([]ReadSnapshot, 0, len(args.CandidateIDs))
	if len(args.CandidateIDs) > 5 {
		return Result{}, invalid("candidate_ids must not contain more than 5 items")
	}
	for _, rawID := range args.CandidateIDs {
		id, err := positiveID(rawID)
		if err != nil {
			return Result{}, err
		}
		snapshot, ok := turn.Reads[id]
		if !ok {
			return Result{}, ErrReadRequired
		}
		candidates = append(candidates, snapshot)
	}
	if args.Kind == "choose_target" && len(candidates) == 0 {
		return Result{}, invalid("choose_target requires candidates read in the current turn")
	}
	if len(args.Draft) > 0 && !json.Valid(args.Draft) {
		return Result{}, invalid("draft must be valid JSON")
	}
	turn.Finished = true
	return Result{Name: "ask_clarification", Payload: clarificationPayload{
		Kind: args.Kind, Question: args.Question, Candidates: candidates, Draft: args.Draft,
	}, StopsTurn: true}, nil
}

type expensePayload struct {
	ID       string           `json:"id"`
	Amount   string           `json:"amount"`
	Category expense.Category `json:"category"`
	SpentAt  string           `json:"spent_at"`
	Comment  *string          `json:"comment"`
	Version  string           `json:"version"`
}

func toExpense(item expense.Expense) expensePayload {
	return expensePayload{
		ID: strconv.FormatInt(item.ID, 10), Amount: item.Amount.String(), Category: item.Category,
		SpentAt: item.SpentAt.String(), Comment: item.Comment, Version: strconv.FormatInt(item.Version, 10),
	}
}

func remember(turn *Turn, item expense.Expense) {
	turn.Reads[item.ID] = ReadSnapshot{
		ID: item.ID, Version: item.Version, Amount: item.Amount, AmountText: item.Amount.String(),
		Category: item.Category, SpentAt: item.SpentAt.String(), Comment: item.Comment,
	}
}

func target(rawID, rawVersion string, turn *Turn) (int64, int64, error) {
	id, err := positiveID(rawID)
	if err != nil {
		return 0, 0, err
	}
	version, err := positiveID(rawVersion)
	if err != nil {
		return 0, 0, err
	}
	snapshot, ok := turn.Reads[id]
	if !ok {
		return 0, 0, ErrReadRequired
	}
	if snapshot.Version != version {
		return 0, 0, expense.ErrVersionConflict
	}
	return id, version, nil
}

func filterFrom(category *expense.Category, dateFrom, dateTo, query string) (expense.ListFilter, error) {
	filter := expense.ListFilter{Category: category, Query: query}
	if dateFrom != "" {
		parsed, err := expense.ParseDate(dateFrom)
		if err != nil {
			return filter, invalid("date_from is invalid")
		}
		filter.DateFrom = &parsed
	}
	if dateTo != "" {
		parsed, err := expense.ParseDate(dateTo)
		if err != nil {
			return filter, invalid("date_to is invalid")
		}
		filter.DateTo = &parsed
	}
	return filter, nil
}

func positiveID(value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, invalid("ID and version values must be positive decimal strings")
	}
	return parsed, nil
}

func decodeArguments(raw json.RawMessage, target any) error {
	if len(raw) == 0 || len(raw) > maxToolArguments {
		return invalid("arguments are empty or too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArguments, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return invalid("arguments must contain one JSON object")
	}
	return nil
}

func invalid(message string) error { return fmt.Errorf("%w: %s", ErrInvalidArguments, message) }
