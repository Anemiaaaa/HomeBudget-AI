package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/expense"
)

const maxBodyBytes = 16 << 10

type expenseAdmin interface {
	Create(context.Context, expense.CreateInput, expense.Date) (expense.Expense, error)
	Get(context.Context, int64) (expense.Expense, error)
	List(context.Context, expense.ListFilter) (expense.ListResult, error)
	Summary(context.Context, expense.ListFilter) (expense.Summary, error)
	Update(context.Context, int64, expense.UpdateInput, expense.Date) (expense.Expense, error)
	Delete(context.Context, int64, int64) error
	ListUsers(context.Context) ([]expense.User, error)
}

type API struct {
	service  expenseAdmin
	location *time.Location
	now      func() time.Time
}

func NewAPI(service expenseAdmin, location *time.Location) *API {
	return &API{service: service, location: location, now: time.Now}
}

func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/meta", a.meta)
	mux.HandleFunc("GET /api/v1/users", a.users)
	mux.HandleFunc("GET /api/v1/expenses", a.list)
	mux.HandleFunc("GET /api/v1/expenses/summary", a.summary)
	mux.HandleFunc("GET /api/v1/expenses/{id}", a.get)
	mux.HandleFunc("POST /api/v1/expenses", a.create)
	mux.HandleFunc("PATCH /api/v1/expenses/{id}", a.update)
	mux.HandleFunc("DELETE /api/v1/expenses/{id}", a.delete)
}

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestID := request.Header.Get("X-Request-ID")
		if len(requestID) == 0 || len(requestID) > 100 {
			requestID = newRequestID()
		}
		writer.Header().Set("X-Request-ID", requestID)
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; frame-ancestors 'none'; object-src 'none'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'")
		next.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), requestIDKey{}, requestID)))
	})
}

func (a *API) meta(writer http.ResponseWriter, _ *http.Request) {
	labels := map[expense.Category]string{
		expense.CategoryFood: "Еда", expense.CategoryTransport: "Транспорт",
		expense.CategoryHousing: "Жильё", expense.CategoryEntertainment: "Развлечения",
		expense.CategoryHealth: "Здоровье", expense.CategoryShopping: "Покупки", expense.CategoryOther: "Другое",
	}
	categories := make([]map[string]string, 0, len(expense.Categories))
	for _, category := range expense.Categories {
		categories = append(categories, map[string]string{"code": string(category), "label": labels[category]})
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"categories": categories, "currency": "RUB", "timezone": a.location.String(),
		"today": expense.DateInLocation(a.now(), a.location).String(),
	})
}

func (a *API) users(writer http.ResponseWriter, request *http.Request) {
	users, err := a.service.ListUsers(request.Context())
	if err != nil {
		a.writeError(writer, request, err)
		return
	}
	result := make([]userResponse, 0, len(users))
	for _, user := range users {
		result = append(result, toUserResponse(user))
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": result})
}

func (a *API) list(writer http.ResponseWriter, request *http.Request) {
	filter, err := parseFilter(request.URL.Query(), true)
	if err != nil {
		a.writeError(writer, request, err)
		return
	}
	result, err := a.service.List(request.Context(), filter)
	if err != nil {
		a.writeError(writer, request, err)
		return
	}
	items := make([]expenseResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, toExpenseResponse(item))
	}
	writeJSON(writer, http.StatusOK, listResponse{Items: items, TotalCount: result.TotalCount, Limit: result.Limit, Offset: result.Offset})
}

func (a *API) summary(writer http.ResponseWriter, request *http.Request) {
	filter, err := parseFilter(request.URL.Query(), false)
	if err != nil {
		a.writeError(writer, request, err)
		return
	}
	result, err := a.service.Summary(request.Context(), filter)
	if err != nil {
		a.writeError(writer, request, err)
		return
	}
	byCategory := make([]categorySummaryResponse, 0, len(result.ByCategory))
	for _, item := range result.ByCategory {
		byCategory = append(byCategory, categorySummaryResponse{Category: item.Category, Amount: item.Amount.String(), Count: item.Count})
	}
	writeJSON(writer, http.StatusOK, summaryResponse{TotalAmount: result.TotalAmount.String(), Count: result.Count, ByCategory: byCategory})
}

func (a *API) get(writer http.ResponseWriter, request *http.Request) {
	id, err := positiveID(request.PathValue("id"), "id")
	if err != nil {
		a.writeError(writer, request, err)
		return
	}
	item, err := a.service.Get(request.Context(), id)
	if err != nil {
		a.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, toExpenseResponse(item))
}

func (a *API) create(writer http.ResponseWriter, request *http.Request) {
	if !sameOrigin(request) {
		a.writeError(writer, request, invalidRequest("origin is not allowed"))
		return
	}
	var body createExpenseRequest
	if err := decodeJSON(writer, request, &body); err != nil {
		a.writeError(writer, request, err)
		return
	}
	userID, err := positiveID(body.UserID, "user_id")
	if err != nil {
		a.writeError(writer, request, err)
		return
	}
	created, err := a.service.Create(request.Context(), expense.CreateInput{
		UserID: userID, Amount: body.Amount, Category: body.Category, SpentAt: body.SpentAt, Comment: body.Comment,
	}, expense.DateInLocation(a.now(), a.location))
	if err != nil {
		a.writeError(writer, request, err)
		return
	}
	writer.Header().Set("Location", "/api/v1/expenses/"+strconv.FormatInt(created.ID, 10))
	writeJSON(writer, http.StatusCreated, toExpenseResponse(created))
}

func (a *API) update(writer http.ResponseWriter, request *http.Request) {
	if !sameOrigin(request) {
		a.writeError(writer, request, invalidRequest("origin is not allowed"))
		return
	}
	id, err := positiveID(request.PathValue("id"), "id")
	if err != nil {
		a.writeError(writer, request, err)
		return
	}
	var body updateExpenseRequest
	if err := decodeJSON(writer, request, &body); err != nil {
		a.writeError(writer, request, err)
		return
	}
	input, err := body.domainInput()
	if err != nil {
		a.writeError(writer, request, err)
		return
	}
	updated, err := a.service.Update(request.Context(), id, input, expense.DateInLocation(a.now(), a.location))
	if err != nil {
		a.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, toExpenseResponse(updated))
}

func (a *API) delete(writer http.ResponseWriter, request *http.Request) {
	if !sameOrigin(request) {
		a.writeError(writer, request, invalidRequest("origin is not allowed"))
		return
	}
	id, err := positiveID(request.PathValue("id"), "id")
	if err != nil {
		a.writeError(writer, request, err)
		return
	}
	version, err := positiveID(request.URL.Query().Get("expected_version"), "expected_version")
	if err != nil {
		a.writeError(writer, request, err)
		return
	}
	if err := a.service.Delete(request.Context(), id, version); err != nil {
		a.writeError(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (a *API) writeError(writer http.ResponseWriter, request *http.Request, err error) {
	status, code, message := http.StatusInternalServerError, "internal_error", "Внутренняя ошибка сервера"
	var fields map[string]string
	var validationError *expense.ValidationError
	switch {
	case errors.As(err, &validationError):
		status, code, message, fields = http.StatusUnprocessableEntity, "validation_error", "Проверьте введённые данные", validationError.Fields
	case errors.Is(err, expense.ErrNotFound):
		status, code, message = http.StatusNotFound, "not_found", "Трата не найдена"
	case errors.Is(err, expense.ErrVersionConflict):
		status, code, message = http.StatusConflict, "version_conflict", "Трата была изменена. Обновите данные и повторите действие"
	case errors.Is(err, errInvalidRequest):
		status, code, message = http.StatusBadRequest, "invalid_request", err.Error()
	case errors.Is(err, errTooLarge):
		status, code, message = http.StatusRequestEntityTooLarge, "request_too_large", "Тело запроса превышает 16 KiB"
	default:
		slog.Error("HTTP request failed", "request_id", requestID(request.Context()), "method", request.Method, "path", request.URL.Path, "error", err)
	}
	writeJSON(writer, status, errorEnvelope{Error: apiError{Code: code, Message: message, Fields: fields, RequestID: requestID(request.Context())}})
}

func parseFilter(values url.Values, pagination bool) (expense.ListFilter, error) {
	filter := expense.ListFilter{Query: values.Get("query")}
	if value := values.Get("user_id"); value != "" {
		parsed, err := positiveID(value, "user_id")
		if err != nil {
			return filter, err
		}
		filter.UserID = &parsed
	}
	if value := values.Get("category"); value != "" {
		category := expense.Category(value)
		filter.Category = &category
	}
	if value := values.Get("date_from"); value != "" {
		parsed, err := expense.ParseDate(value)
		if err != nil {
			return filter, &expense.ValidationError{Fields: map[string]string{"date_from": err.Error()}}
		}
		filter.DateFrom = &parsed
	}
	if value := values.Get("date_to"); value != "" {
		parsed, err := expense.ParseDate(value)
		if err != nil {
			return filter, &expense.ValidationError{Fields: map[string]string{"date_to": err.Error()}}
		}
		filter.DateTo = &parsed
	}
	if pagination {
		filter.Limit = 20
		if value := values.Get("limit"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return filter, &expense.ValidationError{Fields: map[string]string{"limit": "must be an integer"}}
			}
			filter.Limit = parsed
		}
		if value := values.Get("offset"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return filter, &expense.ValidationError{Fields: map[string]string{"offset": "must be an integer"}}
			}
			filter.Offset = parsed
		}
	}
	return filter, nil
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, target any) error {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return invalidRequest("Content-Type must be application/json")
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return errTooLarge
		}
		return invalidRequest("Некорректный JSON: " + err.Error())
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return invalidRequest("Тело должно содержать один JSON-объект")
	}
	return nil
}

func sameOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && strings.EqualFold(parsed.Host, request.Host)
}

func positiveID(value, field string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, &expense.ValidationError{Fields: map[string]string{field: "must be a positive integer"}}
	}
	return parsed, nil
}

type requestIDKey struct{}

func requestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey{}).(string)
	return value
}

func newRequestID() string {
	var bytes [12]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(bytes[:])
}

var errInvalidRequest = errors.New("invalid request")
var errTooLarge = errors.New("request too large")

type invalidRequestError struct{ message string }

func (e invalidRequestError) Error() string        { return e.message }
func (e invalidRequestError) Is(target error) bool { return target == errInvalidRequest }
func invalidRequest(message string) error          { return invalidRequestError{message: message} }

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		slog.Error("encode HTTP response", "error", err)
	}
}
