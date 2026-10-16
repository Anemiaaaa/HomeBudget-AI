package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/expense"
)

type adminStub struct {
	createInput expense.CreateInput
	updateInput expense.UpdateInput
	result      expense.Expense
	err         error
}

func (s *adminStub) Create(_ context.Context, input expense.CreateInput, _ expense.Date) (expense.Expense, error) {
	s.createInput = input
	return s.result, s.err
}
func (s *adminStub) Get(context.Context, int64) (expense.Expense, error) { return s.result, s.err }
func (s *adminStub) List(_ context.Context, filter expense.ListFilter) (expense.ListResult, error) {
	return expense.ListResult{Items: []expense.Expense{}, Limit: filter.Limit, Offset: filter.Offset}, s.err
}
func (s *adminStub) Summary(context.Context, expense.ListFilter) (expense.Summary, error) {
	return expense.Summary{}, s.err
}
func (s *adminStub) Update(_ context.Context, _ int64, input expense.UpdateInput, _ expense.Date) (expense.Expense, error) {
	s.updateInput = input
	return s.result, s.err
}
func (s *adminStub) Delete(context.Context, int64, int64) error { return s.err }
func (s *adminStub) ListUsers(context.Context) ([]expense.User, error) {
	return []expense.User{}, s.err
}

func TestCreateExpenseContract(t *testing.T) {
	t.Parallel()
	amount, _ := expense.ParseMoney("350")
	spentAt, _ := expense.ParseDate("2026-10-15")
	service := &adminStub{result: expense.Expense{
		ID: 7, UserID: 101, Amount: amount, Category: expense.CategoryFood, SpentAt: spentAt,
		CreatedAt: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC), Version: 1,
	}}
	recorder := performRequest(service, http.MethodPost, "/api/v1/expenses", `{"user_id":"101","amount":"350","category":"food","spent_at":"сегодня"}`)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Location") != "/api/v1/expenses/7" {
		t.Fatalf("Location = %q", recorder.Header().Get("Location"))
	}
	if !strings.Contains(recorder.Body.String(), `"id":"7"`) || !strings.Contains(recorder.Body.String(), `"amount":"350.00"`) {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	if service.createInput.UserID != 101 || service.createInput.SpentAt != "сегодня" {
		t.Fatalf("input = %+v", service.createInput)
	}
}

func TestUpdateNullCommentAndVersionConflict(t *testing.T) {
	t.Parallel()
	service := &adminStub{err: expense.ErrVersionConflict}
	recorder := performRequest(service, http.MethodPatch, "/api/v1/expenses/7", `{"expected_version":"3","comment":null}`)

	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"code":"version_conflict"`) {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if service.updateInput.ExpectedVersion != 3 || !service.updateInput.Comment.Set || service.updateInput.Comment.Value != nil {
		t.Fatalf("update input = %+v", service.updateInput)
	}
}

func TestInvalidJSONAndBodyLimit(t *testing.T) {
	t.Parallel()
	service := &adminStub{}

	unknown := performRequest(service, http.MethodPost, "/api/v1/expenses", `{"user_id":"101","amount":"1","category":"food","spent_at":"сегодня","extra":true}`)
	if unknown.Code != http.StatusBadRequest || !strings.Contains(unknown.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("unknown field: status = %d, body = %s", unknown.Code, unknown.Body.String())
	}

	large := performRequest(service, http.MethodPost, "/api/v1/expenses", `{"padding":"`+strings.Repeat("x", maxBodyBytes)+`"}`)
	if large.Code != http.StatusRequestEntityTooLarge || !strings.Contains(large.Body.String(), `"code":"request_too_large"`) {
		t.Fatalf("large body: status = %d, body = %s", large.Code, large.Body.String())
	}
}

func TestOriginAndValidationErrors(t *testing.T) {
	t.Parallel()
	service := &adminStub{err: &expense.ValidationError{Fields: map[string]string{"amount": "invalid"}}}
	recorder := performRequest(service, http.MethodPost, "/api/v1/expenses", `{"user_id":"101","amount":"bad","category":"food","spent_at":"сегодня"}`)
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), `"amount":"invalid"`) {
		t.Fatalf("validation: status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	handler := testHandler(&adminStub{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/expenses", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://example.invalid")
	rejected := httptest.NewRecorder()
	handler.ServeHTTP(rejected, request)
	if rejected.Code != http.StatusBadRequest {
		t.Fatalf("cross-origin status = %d, body = %s", rejected.Code, rejected.Body.String())
	}
}

func performRequest(service *adminStub, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	testHandler(service).ServeHTTP(recorder, request)
	return recorder
}

func testHandler(service *adminStub) http.Handler {
	location := time.FixedZone("test", 3*60*60)
	api := NewAPI(service, location)
	api.now = func() time.Time { return time.Date(2026, 10, 15, 12, 0, 0, 0, location) }
	mux := http.NewServeMux()
	api.Register(mux)
	return Middleware(mux)
}
