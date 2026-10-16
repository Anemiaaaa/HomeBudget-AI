package expense

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"
)

type AdminService struct{ repository Repository }
type OwnerService struct{ repository Repository }

func NewAdminService(repository Repository) *AdminService {
	return &AdminService{repository: repository}
}
func NewOwnerService(repository Repository) *OwnerService {
	return &OwnerService{repository: repository}
}

func (s *AdminService) Create(ctx context.Context, input CreateInput, reference Date) (Expense, error) {
	fields := CreateFields{Amount: input.Amount, Category: input.Category, SpentAt: input.SpentAt, Comment: input.Comment}
	return create(ctx, s.repository, input.UserID, fields, reference)
}

func (s *OwnerService) Create(ctx context.Context, ownerID int64, input CreateFields, reference Date) (Expense, error) {
	return create(ctx, s.repository, ownerID, input, reference)
}

func create(ctx context.Context, repository Repository, userID int64, input CreateFields, reference Date) (Expense, error) {
	command, err := validateCreate(userID, input, reference)
	if err != nil {
		return Expense{}, err
	}
	return repository.Create(ctx, command)
}

func (s *AdminService) Get(ctx context.Context, id int64) (Expense, error) {
	if id <= 0 {
		return Expense{}, validation("id", "must be positive")
	}
	return s.repository.Get(ctx, Scope{}, id)
}

func (s *OwnerService) Get(ctx context.Context, ownerID, id int64) (Expense, error) {
	if ownerID <= 0 {
		return Expense{}, validation("user_id", "must be positive")
	}
	if id <= 0 {
		return Expense{}, validation("id", "must be positive")
	}
	return s.repository.Get(ctx, ownerScope(ownerID), id)
}

func (s *AdminService) List(ctx context.Context, filter ListFilter) (ListResult, error) {
	if err := validateFilter(&filter); err != nil {
		return ListResult{}, err
	}
	return s.repository.List(ctx, filter)
}

func (s *OwnerService) List(ctx context.Context, ownerID int64, filter ListFilter) (ListResult, error) {
	if ownerID <= 0 {
		return ListResult{}, validation("user_id", "must be positive")
	}
	filter.UserID = &ownerID
	if err := validateFilter(&filter); err != nil {
		return ListResult{}, err
	}
	return s.repository.List(ctx, filter)
}

func (s *AdminService) Summary(ctx context.Context, filter ListFilter) (Summary, error) {
	if err := validateSummaryFilter(&filter); err != nil {
		return Summary{}, err
	}
	return s.repository.Summary(ctx, filter)
}

func (s *OwnerService) Summary(ctx context.Context, ownerID int64, filter ListFilter) (Summary, error) {
	if ownerID <= 0 {
		return Summary{}, validation("user_id", "must be positive")
	}
	filter.UserID = &ownerID
	if err := validateSummaryFilter(&filter); err != nil {
		return Summary{}, err
	}
	return s.repository.Summary(ctx, filter)
}

func (s *AdminService) Update(ctx context.Context, id int64, input UpdateInput, reference Date) (Expense, error) {
	if id <= 0 {
		return Expense{}, validation("id", "must be positive")
	}
	command, err := validateUpdate(input, reference, true)
	if err != nil {
		return Expense{}, err
	}
	return s.repository.Update(ctx, Scope{}, id, command)
}

func (s *OwnerService) Update(ctx context.Context, ownerID, id int64, input UpdateInput, reference Date) (Expense, error) {
	if ownerID <= 0 {
		return Expense{}, validation("user_id", "must be positive")
	}
	if id <= 0 {
		return Expense{}, validation("id", "must be positive")
	}
	command, err := validateUpdate(input, reference, false)
	if err != nil {
		return Expense{}, err
	}
	return s.repository.Update(ctx, ownerScope(ownerID), id, command)
}

func (s *AdminService) Delete(ctx context.Context, id, expectedVersion int64) error {
	if id <= 0 {
		return validation("id", "must be positive")
	}
	if expectedVersion <= 0 {
		return validation("expected_version", "must be positive")
	}
	return s.repository.Delete(ctx, Scope{}, id, expectedVersion)
}

func (s *OwnerService) Delete(ctx context.Context, ownerID, id, expectedVersion int64) error {
	if ownerID <= 0 {
		return validation("user_id", "must be positive")
	}
	if id <= 0 {
		return validation("id", "must be positive")
	}
	if expectedVersion <= 0 {
		return validation("expected_version", "must be positive")
	}
	return s.repository.Delete(ctx, ownerScope(ownerID), id, expectedVersion)
}

func (s *AdminService) ListUsers(ctx context.Context) ([]User, error) {
	return s.repository.ListUsers(ctx)
}

func validateCreate(userID int64, input CreateFields, reference Date) (NewExpense, error) {
	fields := map[string]string{}
	if userID <= 0 {
		fields["user_id"] = "must be positive"
	}
	amount, err := ParseMoney(input.Amount)
	if err != nil {
		fields["amount"] = err.Error()
	}
	if err := ValidateCategory(input.Category); err != nil {
		fields["category"] = err.Error()
	}
	spentAt, err := ResolveRelativeDate(input.SpentAt, reference)
	if err != nil {
		fields["spent_at"] = err.Error()
	} else if spentAt.After(reference) {
		fields["spent_at"] = "must not be in the future"
	}
	comment, err := normalizeComment(input.Comment)
	if err != nil {
		fields["comment"] = err.Error()
	}
	if len(fields) > 0 {
		return NewExpense{}, &ValidationError{Fields: fields}
	}
	return NewExpense{UserID: userID, Amount: amount, Category: input.Category, SpentAt: spentAt, Comment: comment}, nil
}

func validateUpdate(input UpdateInput, reference Date, allowOwnerChange bool) (UpdateCommand, error) {
	fields := map[string]string{}
	command := UpdateCommand{ExpectedVersion: input.ExpectedVersion}
	if input.ExpectedVersion <= 0 {
		fields["expected_version"] = "must be positive"
	}
	changed := false
	if input.UserID.Set {
		changed = true
		if !allowOwnerChange {
			fields["user_id"] = "cannot be changed from the owner scope"
		} else if input.UserID.Value <= 0 {
			fields["user_id"] = "must be positive"
		} else {
			command.UserID = input.UserID
		}
	}
	if input.Amount.Set {
		changed = true
		amount, err := ParseMoney(input.Amount.Value)
		if err != nil {
			fields["amount"] = err.Error()
		} else {
			command.Amount = Optional[Money]{Set: true, Value: amount}
		}
	}
	if input.Category.Set {
		changed = true
		if err := ValidateCategory(input.Category.Value); err != nil {
			fields["category"] = err.Error()
		} else {
			command.Category = input.Category
		}
	}
	if input.SpentAt.Set {
		changed = true
		date, err := ResolveRelativeDate(input.SpentAt.Value, reference)
		if err != nil {
			fields["spent_at"] = err.Error()
		} else if date.After(reference) {
			fields["spent_at"] = "must not be in the future"
		} else {
			command.SpentAt = Optional[Date]{Set: true, Value: date}
		}
	}
	if input.Comment.Set {
		changed = true
		comment, err := normalizeComment(input.Comment.Value)
		if err != nil {
			fields["comment"] = err.Error()
		} else {
			command.Comment = Optional[*string]{Set: true, Value: comment}
		}
	}
	if !changed {
		fields["body"] = "at least one expense field must be provided"
	}
	if len(fields) > 0 {
		return UpdateCommand{}, &ValidationError{Fields: fields}
	}
	return command, nil
}

func validateFilter(filter *ListFilter) error {
	if filter.Limit == 0 {
		filter.Limit = 20
	}
	if filter.Limit < 1 || filter.Limit > 100 {
		return validation("limit", "must be between 1 and 100")
	}
	if filter.Offset < 0 {
		return validation("offset", "must not be negative")
	}
	return validateSummaryFilter(filter)
}

func validateSummaryFilter(filter *ListFilter) error {
	if filter.UserID != nil && *filter.UserID <= 0 {
		return validation("user_id", "must be positive")
	}
	if filter.Category != nil {
		if err := ValidateCategory(*filter.Category); err != nil {
			return validation("category", err.Error())
		}
	}
	if filter.DateFrom != nil && filter.DateTo != nil {
		if err := ValidatePeriod(Period{From: *filter.DateFrom, To: *filter.DateTo}); err != nil {
			return validation("date_from", err.Error())
		}
	}
	filter.Query = strings.TrimSpace(filter.Query)
	if utf8.RuneCountInString(filter.Query) > 200 {
		return validation("query", "must not exceed 200 characters")
	}
	return nil
}

func ValidateCategory(category Category) error {
	for _, allowed := range Categories {
		if category == allowed {
			return nil
		}
	}
	return errors.New("unsupported expense category")
}

func normalizeComment(comment *string) (*string, error) {
	if comment == nil {
		return nil, nil
	}
	normalized := strings.TrimSpace(*comment)
	if normalized == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(normalized) > 500 {
		return nil, errors.New("must not exceed 500 characters")
	}
	return &normalized, nil
}

func ownerScope(ownerID int64) Scope { return Scope{OwnerID: &ownerID} }

func validation(field, message string) error {
	return &ValidationError{Fields: map[string]string{field: message}}
}
