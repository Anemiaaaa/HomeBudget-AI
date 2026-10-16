package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/expense"
)

type ownerExpensesStub struct {
	items     []expense.Expense
	lastOwner int64
	creates   int
	updates   int
}

func (s *ownerExpensesStub) Create(_ context.Context, ownerID int64, fields expense.CreateFields, reference expense.Date) (expense.Expense, error) {
	s.lastOwner, s.creates = ownerID, s.creates+1
	amount, err := expense.ParseMoney(fields.Amount)
	if err != nil {
		return expense.Expense{}, err
	}
	date, err := expense.ResolveRelativeDate(fields.SpentAt, reference)
	if err != nil {
		return expense.Expense{}, err
	}
	return expense.Expense{ID: 9, UserID: ownerID, Amount: amount, Category: fields.Category, SpentAt: date, Comment: fields.Comment, Version: 1}, nil
}

func (s *ownerExpensesStub) Get(_ context.Context, ownerID, id int64) (expense.Expense, error) {
	s.lastOwner = ownerID
	for _, item := range s.items {
		if item.ID == id && item.UserID == ownerID {
			return item, nil
		}
	}
	return expense.Expense{}, expense.ErrNotFound
}

func (s *ownerExpensesStub) List(_ context.Context, ownerID int64, filter expense.ListFilter) (expense.ListResult, error) {
	s.lastOwner = ownerID
	result := make([]expense.Expense, 0)
	for _, item := range s.items {
		if item.UserID == ownerID {
			result = append(result, item)
		}
	}
	return expense.ListResult{Items: result, TotalCount: int64(len(result)), Limit: filter.Limit, Offset: filter.Offset}, nil
}

func (s *ownerExpensesStub) Summary(_ context.Context, ownerID int64, _ expense.ListFilter) (expense.Summary, error) {
	s.lastOwner = ownerID
	return expense.Summary{TotalAmount: mustMoney("350"), Count: 1}, nil
}

func (s *ownerExpensesStub) Update(_ context.Context, ownerID, id int64, input expense.UpdateInput, _ expense.Date) (expense.Expense, error) {
	s.lastOwner, s.updates = ownerID, s.updates+1
	item, err := s.Get(context.Background(), ownerID, id)
	if err != nil {
		return expense.Expense{}, err
	}
	if input.Amount.Set {
		item.Amount, err = expense.ParseMoney(input.Amount.Value)
		if err != nil {
			return expense.Expense{}, err
		}
	}
	item.Version++
	return item, nil
}

func TestDefinitionsExcludeTrustedScope(t *testing.T) {
	t.Parallel()
	encoded, err := json.Marshal(Definitions())
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if strings.Contains(text, "user_id") || strings.Contains(text, "admin") || strings.Contains(text, "role") {
		t.Fatalf("tool definitions expose trusted scope: %s", text)
	}
	if len(Definitions()) != 6 || strings.Count(text, `"additionalProperties":false`) < 7 {
		t.Fatalf("tool definitions are not strict: %s", text)
	}
}

func TestCreateUsesTrustedOwnerAndStopsTurn(t *testing.T) {
	t.Parallel()
	stub := &ownerExpensesStub{}
	executor := NewExecutor(stub)
	turn := &Turn{OwnerID: 101, ReferenceDate: mustDate("2026-10-15")}

	result, err := executor.Execute(context.Background(), turn, Call{Name: "create_expense", Arguments: json.RawMessage(`{
		"amount":"350","category":"food","spent_at":"сегодня","comment":"кофе"
	}`)})
	if err != nil {
		t.Fatalf("Execute(create) error = %v", err)
	}
	if stub.lastOwner != 101 || stub.creates != 1 || !result.Mutated || !result.StopsTurn || !turn.Finished {
		t.Fatalf("result = %+v, stub = %+v, turn = %+v", result, stub, turn)
	}
	_, err = executor.Execute(context.Background(), turn, Call{Name: "list_expenses", Arguments: json.RawMessage(`{}`)})
	if !errors.Is(err, ErrMutationLimit) {
		t.Fatalf("second call error = %v, want ErrMutationLimit", err)
	}
}

func TestArgumentsRejectUnknownFields(t *testing.T) {
	t.Parallel()
	stub := &ownerExpensesStub{}
	_, err := NewExecutor(stub).Execute(context.Background(), &Turn{OwnerID: 101, ReferenceDate: mustDate("2026-10-15")}, Call{
		Name: "create_expense", Arguments: json.RawMessage(`{"amount":"1","category":"food","spent_at":"сегодня","user_id":"202"}`),
	})
	if !errors.Is(err, ErrInvalidArguments) || stub.creates != 0 {
		t.Fatalf("error = %v, creates = %d", err, stub.creates)
	}
}

func TestUpdateRequiresCurrentReadAndExpectedOldAmount(t *testing.T) {
	t.Parallel()
	item := expense.Expense{ID: 7, UserID: 101, Amount: mustMoney("350"), Category: expense.CategoryFood, SpentAt: mustDate("2026-10-15"), Version: 3}
	stub := &ownerExpensesStub{items: []expense.Expense{item}}
	executor := NewExecutor(stub)
	arguments := json.RawMessage(`{"expense_id":"7","expected_version":"3","expected_old_amount":"350","patch":{"amount":"450"}}`)

	_, err := executor.Execute(context.Background(), &Turn{OwnerID: 101, ReferenceDate: mustDate("2026-10-15")}, Call{Name: "update_expense", Arguments: arguments})
	if !errors.Is(err, ErrReadRequired) || stub.updates != 0 {
		t.Fatalf("update without read: error = %v, updates = %d", err, stub.updates)
	}

	turn := &Turn{OwnerID: 101, ReferenceDate: mustDate("2026-10-15")}
	if _, err := executor.Execute(context.Background(), turn, Call{Name: "list_expenses", Arguments: json.RawMessage(`{"expense_id":"7"}`)}); err != nil {
		t.Fatalf("list error = %v", err)
	}
	result, err := executor.Execute(context.Background(), turn, Call{Name: "update_expense", Arguments: arguments})
	if err != nil {
		t.Fatalf("update error = %v", err)
	}
	if !result.Mutated || stub.updates != 1 || stub.lastOwner != 101 {
		t.Fatalf("result = %+v, stub = %+v", result, stub)
	}
}

func TestUpdateRejectsStaleReadAndOldAmountMismatch(t *testing.T) {
	t.Parallel()
	item := expense.Expense{ID: 7, UserID: 101, Amount: mustMoney("350"), Category: expense.CategoryFood, SpentAt: mustDate("2026-10-15"), Version: 3}
	stub := &ownerExpensesStub{items: []expense.Expense{item}}
	executor := NewExecutor(stub)

	stale := &Turn{OwnerID: 101, ReferenceDate: mustDate("2026-10-15"), Reads: map[int64]ReadSnapshot{7: {ID: 7, Version: 2, Amount: item.Amount}}}
	_, err := executor.Execute(context.Background(), stale, Call{Name: "update_expense", Arguments: json.RawMessage(`{"expense_id":"7","expected_version":"3","patch":{"amount":"450"}}`)})
	if !errors.Is(err, expense.ErrVersionConflict) {
		t.Fatalf("stale error = %v", err)
	}

	mismatch := &Turn{OwnerID: 101, ReferenceDate: mustDate("2026-10-15"), Reads: map[int64]ReadSnapshot{7: {ID: 7, Version: 3, Amount: item.Amount}}}
	_, err = executor.Execute(context.Background(), mismatch, Call{Name: "update_expense", Arguments: json.RawMessage(`{"expense_id":"7","expected_version":"3","expected_old_amount":"300","patch":{"amount":"450"}}`)})
	if !errors.Is(err, ErrOldAmountMismatch) {
		t.Fatalf("old amount error = %v", err)
	}
}

func TestDeleteOnlyPreparesConfirmation(t *testing.T) {
	t.Parallel()
	item := expense.Expense{ID: 7, UserID: 101, Amount: mustMoney("1200"), Category: expense.CategoryTransport, SpentAt: mustDate("2026-10-14"), Version: 2}
	stub := &ownerExpensesStub{items: []expense.Expense{item}}
	executor := NewExecutor(stub)
	turn := &Turn{OwnerID: 101, ReferenceDate: mustDate("2026-10-15"), Reads: map[int64]ReadSnapshot{7: {ID: 7, Version: 2, Amount: item.Amount}}}

	result, err := executor.Execute(context.Background(), turn, Call{Name: "delete_expense", Arguments: json.RawMessage(`{"expense_id":"7","expected_version":"2"}`)})
	if err != nil {
		t.Fatalf("delete preparation error = %v", err)
	}
	payload, ok := result.Payload.(deletePayload)
	if !ok || !payload.ConfirmationRequired || result.Mutated || !result.StopsTurn {
		t.Fatalf("result = %+v", result)
	}
}

func TestListLimitAndClarificationCandidates(t *testing.T) {
	t.Parallel()
	item := expense.Expense{ID: 7, UserID: 101, Amount: mustMoney("10"), Category: expense.CategoryOther, SpentAt: mustDate("2026-10-15"), Version: 1}
	executor := NewExecutor(&ownerExpensesStub{items: []expense.Expense{item}})
	turn := &Turn{OwnerID: 101, ReferenceDate: mustDate("2026-10-15")}

	_, err := executor.Execute(context.Background(), turn, Call{Name: "list_expenses", Arguments: json.RawMessage(`{"limit":21}`)})
	if !errors.Is(err, ErrInvalidArguments) {
		t.Fatalf("limit error = %v", err)
	}
	if _, err := executor.Execute(context.Background(), turn, Call{Name: "list_expenses", Arguments: json.RawMessage(`{"expense_id":"7"}`)}); err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), turn, Call{Name: "ask_clarification", Arguments: json.RawMessage(`{
		"kind":"choose_target","question":"Какую запись изменить?","candidate_ids":["7"],
		"draft":{"intent":"update","patch":{"amount":"20"}}
	}`)})
	if err != nil || !result.StopsTurn || !turn.Finished {
		t.Fatalf("clarification result = %+v, error = %v", result, err)
	}
}

func TestClarificationDraftIsStrictAndMatchesKind(t *testing.T) {
	t.Parallel()
	item := expense.Expense{ID: 7, UserID: 101, Amount: mustMoney("10"), Category: expense.CategoryOther, SpentAt: mustDate("2026-10-15"), Version: 1}
	executor := NewExecutor(&ownerExpensesStub{items: []expense.Expense{item}})

	tests := []struct {
		name string
		raw  string
	}{
		{"missing draft", `{"kind":"clarify_create","question":"Какая сумма?"}`},
		{"unknown draft field", `{"kind":"clarify_create","question":"Какая сумма?","draft":{"user_id":"202"}}`},
		{"request without source", `{"kind":"clarify_request","question":"Что сделать?","draft":{}}`},
		{"target without intent", `{"kind":"choose_target","question":"Какую?","candidate_ids":["7"],"draft":{"patch":{"amount":"20"}}}`},
		{"delete with patch", `{"kind":"choose_target","question":"Какую?","candidate_ids":["7"],"draft":{"intent":"delete","patch":{"amount":"20"}}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			turn := &Turn{OwnerID: 101, ReferenceDate: mustDate("2026-10-15"), Reads: map[int64]ReadSnapshot{
				7: {ID: 7, Version: 1, Amount: item.Amount},
			}}
			_, err := executor.Execute(context.Background(), turn, Call{Name: "ask_clarification", Arguments: json.RawMessage(test.raw)})
			if !errors.Is(err, ErrInvalidArguments) {
				t.Fatalf("error = %v, want ErrInvalidArguments", err)
			}
		})
	}
}

func mustMoney(value string) expense.Money {
	money, err := expense.ParseMoney(value)
	if err != nil {
		panic(err)
	}
	return money
}

func mustDate(value string) expense.Date {
	date, err := expense.ParseDate(value)
	if err != nil {
		panic(err)
	}
	return date
}
