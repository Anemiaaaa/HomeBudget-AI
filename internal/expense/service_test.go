package expense

import (
	"context"
	"errors"
	"testing"
)

type repositoryStub struct {
	created     []NewExpense
	getScope    Scope
	listFilter  ListFilter
	updateScope Scope
	deleteScope Scope
}

func (r *repositoryStub) Create(_ context.Context, input NewExpense) (Expense, error) {
	r.created = append(r.created, input)
	return Expense{ID: 1, UserID: input.UserID, Amount: input.Amount, Category: input.Category, SpentAt: input.SpentAt, Comment: input.Comment, Version: 1}, nil
}

func (r *repositoryStub) CreateMany(ctx context.Context, inputs []NewExpense) ([]Expense, error) {
	created := make([]Expense, 0, len(inputs))
	for _, input := range inputs {
		item, err := r.Create(ctx, input)
		if err != nil {
			return nil, err
		}
		item.ID = int64(len(created) + 1)
		created = append(created, item)
	}
	return created, nil
}

func (r *repositoryStub) Get(_ context.Context, scope Scope, id int64) (Expense, error) {
	r.getScope = scope
	return Expense{ID: id, UserID: scopeOwner(scope), Version: 1}, nil
}

func (r *repositoryStub) List(_ context.Context, filter ListFilter) (ListResult, error) {
	r.listFilter = filter
	return ListResult{Limit: filter.Limit, Offset: filter.Offset}, nil
}

func (r *repositoryStub) Summary(_ context.Context, filter ListFilter) (Summary, error) {
	r.listFilter = filter
	return Summary{}, nil
}

func (r *repositoryStub) Update(_ context.Context, scope Scope, id int64, command UpdateCommand) (Expense, error) {
	r.updateScope = scope
	return Expense{ID: id, UserID: scopeOwner(scope), Version: command.ExpectedVersion + 1}, nil
}

func (r *repositoryStub) Delete(_ context.Context, scope Scope, _, _ int64) error {
	r.deleteScope = scope
	return nil
}

func (r *repositoryStub) ListUsers(context.Context) ([]User, error) { return nil, nil }

func TestAdminCreateNormalizesExpense(t *testing.T) {
	t.Parallel()
	repository := &repositoryStub{}
	service := NewAdminService(repository)
	reference := mustDate(t, "2026-10-15")
	comment := " кофе "

	created, err := service.Create(context.Background(), CreateInput{
		UserID: 101, Amount: "350", Category: CategoryFood, SpentAt: "сегодня", Comment: &comment,
	}, reference)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Amount.String() != "350.00" || created.SpentAt.String() != "2026-10-15" || created.Comment == nil || *created.Comment != "кофе" {
		t.Fatalf("Create() = %+v", created)
	}
}

func TestOwnerCreateManyValidatesWholeBatchBeforeWriting(t *testing.T) {
	t.Parallel()
	repository := &repositoryStub{}
	service := NewOwnerService(repository)
	reference := mustDate(t, "2026-10-15")

	created, err := service.CreateMany(context.Background(), 101, []CreateFields{
		{Amount: "100", Category: CategoryFood, SpentAt: "сегодня"},
		{Amount: "200", Category: CategoryTransport, SpentAt: "вчера"},
	}, reference)
	if err != nil || len(created) != 2 || len(repository.created) != 2 {
		t.Fatalf("CreateMany() = %+v, %v; repository writes = %d", created, err, len(repository.created))
	}

	repository.created = nil
	_, err = service.CreateMany(context.Background(), 101, []CreateFields{
		{Amount: "100", Category: CategoryFood, SpentAt: "сегодня"},
		{Amount: "0", Category: CategoryTransport, SpentAt: "сегодня"},
	}, reference)
	assertValidationField(t, err, "expenses[1].amount")
	if len(repository.created) != 0 {
		t.Fatalf("invalid batch wrote %d expenses", len(repository.created))
	}
}

func TestOwnerServiceAlwaysAppliesOwnerScope(t *testing.T) {
	t.Parallel()
	repository := &repositoryStub{}
	service := NewOwnerService(repository)
	reference := mustDate(t, "2026-10-15")
	foreignUser := int64(202)

	if _, err := service.List(context.Background(), 101, ListFilter{UserID: &foreignUser}); err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if repository.listFilter.UserID == nil || *repository.listFilter.UserID != 101 {
		t.Fatalf("List() user = %v, want 101", repository.listFilter.UserID)
	}
	if _, err := service.Get(context.Background(), 101, 7); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	assertScopeOwner(t, repository.getScope, 101)
	if _, err := service.Update(context.Background(), 101, 7, UpdateInput{
		ExpectedVersion: 1,
		Comment:         Optional[*string]{Set: true, Value: nil},
	}, reference); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	assertScopeOwner(t, repository.updateScope, 101)
	if err := service.Delete(context.Background(), 101, 7, 2); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	assertScopeOwner(t, repository.deleteScope, 101)
}

func TestOwnerServiceRejectsChangingOwner(t *testing.T) {
	t.Parallel()
	service := NewOwnerService(&repositoryStub{})
	_, err := service.Update(context.Background(), 101, 7, UpdateInput{
		ExpectedVersion: 1,
		UserID:          Optional[int64]{Set: true, Value: 202},
	}, mustDate(t, "2026-10-15"))
	assertValidationField(t, err, "user_id")
}

func TestServiceValidations(t *testing.T) {
	t.Parallel()
	service := NewAdminService(&repositoryStub{})
	reference := mustDate(t, "2026-10-15")

	_, err := service.Create(context.Background(), CreateInput{
		UserID: 101, Amount: "1", Category: CategoryOther, SpentAt: "2026-10-16",
	}, reference)
	assertValidationField(t, err, "spent_at")

	_, err = service.Update(context.Background(), 7, UpdateInput{ExpectedVersion: 1}, reference)
	assertValidationField(t, err, "body")

	from, to := mustDate(t, "2026-10-15"), mustDate(t, "2026-10-14")
	_, err = service.List(context.Background(), ListFilter{DateFrom: &from, DateTo: &to})
	assertValidationField(t, err, "date_from")

	_, err = service.List(context.Background(), ListFilter{Limit: 101})
	assertValidationField(t, err, "limit")
}

func TestAdminUpdatePassesOptimisticVersion(t *testing.T) {
	t.Parallel()
	repository := &repositoryStub{}
	service := NewAdminService(repository)
	result, err := service.Update(context.Background(), 7, UpdateInput{
		ExpectedVersion: 4,
		Amount:          Optional[string]{Set: true, Value: "19.90"},
	}, mustDate(t, "2026-10-15"))
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if result.Version != 5 || repository.updateScope.OwnerID != nil {
		t.Fatalf("Update() result = %+v, scope = %+v", result, repository.updateScope)
	}
}

func mustDate(t *testing.T, value string) Date {
	t.Helper()
	date, err := ParseDate(value)
	if err != nil {
		t.Fatal(err)
	}
	return date
}

func scopeOwner(scope Scope) int64 {
	if scope.OwnerID == nil {
		return 0
	}
	return *scope.OwnerID
}

func assertScopeOwner(t *testing.T, scope Scope, want int64) {
	t.Helper()
	if scope.OwnerID == nil || *scope.OwnerID != want {
		t.Fatalf("scope owner = %v, want %d", scope.OwnerID, want)
	}
}

func assertValidationField(t *testing.T, err error, field string) {
	t.Helper()
	var validationError *ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("error = %v, want ValidationError", err)
	}
	if _, ok := validationError.Fields[field]; !ok {
		t.Fatalf("validation fields = %v, want %q", validationError.Fields, field)
	}
}
