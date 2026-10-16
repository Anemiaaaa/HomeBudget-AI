package httpapi

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/expense"
)

type expenseResponse struct {
	ID        string           `json:"id"`
	UserID    string           `json:"user_id"`
	Amount    string           `json:"amount"`
	Category  expense.Category `json:"category"`
	SpentAt   string           `json:"spent_at"`
	Comment   *string          `json:"comment"`
	CreatedAt string           `json:"created_at"`
	UpdatedAt string           `json:"updated_at"`
	Version   string           `json:"version"`
}

type userResponse struct {
	ID          string  `json:"id"`
	DisplayName *string `json:"display_name"`
	CreatedAt   string  `json:"created_at"`
}

type listResponse struct {
	Items      []expenseResponse `json:"items"`
	TotalCount int64             `json:"total_count"`
	Limit      int               `json:"limit"`
	Offset     int               `json:"offset"`
}

type categorySummaryResponse struct {
	Category expense.Category `json:"category"`
	Amount   string           `json:"amount"`
	Count    int64            `json:"count"`
}

type summaryResponse struct {
	TotalAmount string                    `json:"total_amount"`
	Count       int64                     `json:"count"`
	ByCategory  []categorySummaryResponse `json:"by_category"`
}

type createExpenseRequest struct {
	UserID   string           `json:"user_id"`
	Amount   string           `json:"amount"`
	Category expense.Category `json:"category"`
	SpentAt  string           `json:"spent_at"`
	Comment  *string          `json:"comment,omitempty"`
}

type optionalJSON[T any] struct {
	Set   bool
	Value T
}

func (o *optionalJSON[T]) UnmarshalJSON(data []byte) error {
	o.Set = true
	return json.Unmarshal(data, &o.Value)
}

type updateExpenseRequest struct {
	ExpectedVersion string                         `json:"expected_version"`
	UserID          optionalJSON[string]           `json:"user_id"`
	Amount          optionalJSON[string]           `json:"amount"`
	Category        optionalJSON[expense.Category] `json:"category"`
	SpentAt         optionalJSON[string]           `json:"spent_at"`
	Comment         optionalJSON[*string]          `json:"comment"`
}

func (r updateExpenseRequest) domainInput() (expense.UpdateInput, error) {
	version, err := positiveID(r.ExpectedVersion, "expected_version")
	if err != nil {
		return expense.UpdateInput{}, err
	}
	input := expense.UpdateInput{ExpectedVersion: version}
	if r.UserID.Set {
		userID, err := positiveID(r.UserID.Value, "user_id")
		if err != nil {
			return expense.UpdateInput{}, err
		}
		input.UserID = expense.Optional[int64]{Set: true, Value: userID}
	}
	input.Amount = expense.Optional[string]{Set: r.Amount.Set, Value: r.Amount.Value}
	input.Category = expense.Optional[expense.Category]{Set: r.Category.Set, Value: r.Category.Value}
	input.SpentAt = expense.Optional[string]{Set: r.SpentAt.Set, Value: r.SpentAt.Value}
	input.Comment = expense.Optional[*string]{Set: r.Comment.Set, Value: r.Comment.Value}
	return input, nil
}

func toExpenseResponse(item expense.Expense) expenseResponse {
	return expenseResponse{
		ID: strconv.FormatInt(item.ID, 10), UserID: strconv.FormatInt(item.UserID, 10),
		Amount: item.Amount.String(), Category: item.Category, SpentAt: item.SpentAt.String(), Comment: item.Comment,
		CreatedAt: item.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: item.UpdatedAt.UTC().Format(time.RFC3339Nano),
		Version: strconv.FormatInt(item.Version, 10),
	}
}

func toUserResponse(user expense.User) userResponse {
	return userResponse{ID: strconv.FormatInt(user.ID, 10), DisplayName: user.DisplayName, CreatedAt: user.CreatedAt.UTC().Format(time.RFC3339Nano)}
}

type apiError struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Fields    map[string]string `json:"fields,omitempty"`
	RequestID string            `json:"request_id"`
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}
