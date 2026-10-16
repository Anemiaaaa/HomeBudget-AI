//go:build integration

package postgres

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/expense"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestExpenseRepositoryCRUDScopesFiltersAndSummary(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, cleanupErr := pool.Exec(context.Background(), "TRUNCATE expenses, users RESTART IDENTITY CASCADE")
		pool.Close()
		if cleanupErr != nil {
			t.Errorf("clean integration fixtures: %v", cleanupErr)
		}
	})
	if _, err := pool.Exec(ctx, "TRUNCATE expenses, users RESTART IDENTITY CASCADE"); err != nil {
		t.Fatal(err)
	}

	repository := NewExpenseRepository(pool)
	admin := expense.NewAdminService(repository)
	owner := expense.NewOwnerService(repository)
	reference := integrationDate(t, "2026-10-15")
	coffee, taxi := "Утренний кофе", "Такси домой"
	first, err := admin.Create(ctx, expense.CreateInput{
		UserID: 101, Amount: "350.25", Category: expense.CategoryFood, SpentAt: "сегодня", Comment: &coffee,
	}, reference)
	if err != nil {
		t.Fatalf("Create(user 101) error = %v", err)
	}
	if _, err := admin.Create(ctx, expense.CreateInput{
		UserID: 202, Amount: "1200", Category: expense.CategoryTransport, SpentAt: "вчера", Comment: &taxi,
	}, reference); err != nil {
		t.Fatalf("Create(user 202) error = %v", err)
	}

	ownerList, err := owner.List(ctx, 101, expense.ListFilter{Limit: 20})
	if err != nil {
		t.Fatalf("List(owner 101) error = %v", err)
	}
	if ownerList.TotalCount != 1 || len(ownerList.Items) != 1 || ownerList.Items[0].ID != first.ID {
		t.Fatalf("List(owner 101) = %+v", ownerList)
	}
	if _, err := owner.Get(ctx, 202, first.ID); !errors.Is(err, expense.ErrNotFound) {
		t.Fatalf("Get(foreign owner) error = %v, want ErrNotFound", err)
	}

	food := expense.CategoryFood
	filtered, err := admin.List(ctx, expense.ListFilter{Category: &food, Query: "кофе", Limit: 20})
	if err != nil {
		t.Fatalf("List(filtered) error = %v", err)
	}
	if filtered.TotalCount != 1 || len(filtered.Items) != 1 || filtered.Items[0].ID != first.ID {
		t.Fatalf("List(filtered) = %+v", filtered)
	}
	percentSearch, err := admin.List(ctx, expense.ListFilter{Query: "%", Limit: 20})
	if err != nil {
		t.Fatalf("List(literal percent) error = %v", err)
	}
	if percentSearch.TotalCount != 0 {
		t.Fatalf("literal percent matched %d rows, want 0", percentSearch.TotalCount)
	}

	summary, err := admin.Summary(ctx, expense.ListFilter{})
	if err != nil {
		t.Fatalf("Summary() error = %v", err)
	}
	if summary.TotalAmount.String() != "1550.25" || summary.Count != 2 || len(summary.ByCategory) != 2 {
		t.Fatalf("Summary() = %+v", summary)
	}

	updated, err := admin.Update(ctx, first.ID, expense.UpdateInput{
		ExpectedVersion: first.Version,
		Amount:          expense.Optional[string]{Set: true, Value: "375.50"},
		Comment:         expense.Optional[*string]{Set: true, Value: nil},
	}, reference)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Version != 2 || updated.Amount.String() != "375.50" || updated.Comment != nil {
		t.Fatalf("Update() = %+v", updated)
	}
	if _, err := admin.Update(ctx, first.ID, expense.UpdateInput{
		ExpectedVersion: first.Version,
		Amount:          expense.Optional[string]{Set: true, Value: "400"},
	}, reference); !errors.Is(err, expense.ErrVersionConflict) {
		t.Fatalf("stale Update() error = %v, want ErrVersionConflict", err)
	}
	if err := admin.Delete(ctx, first.ID, first.Version); !errors.Is(err, expense.ErrVersionConflict) {
		t.Fatalf("stale Delete() error = %v, want ErrVersionConflict", err)
	}
	if err := admin.Delete(ctx, first.ID, updated.Version); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := admin.Get(ctx, first.ID); !errors.Is(err, expense.ErrNotFound) {
		t.Fatalf("Get(deleted) error = %v, want ErrNotFound", err)
	}

	users, err := admin.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	if len(users) != 2 || users[0].ID != 101 || users[1].ID != 202 {
		t.Fatalf("ListUsers() = %+v", users)
	}
}

func integrationDate(t *testing.T, value string) expense.Date {
	t.Helper()
	date, err := expense.ParseDate(value)
	if err != nil {
		t.Fatal(err)
	}
	return date
}
