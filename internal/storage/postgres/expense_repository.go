package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/expense"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const expenseColumns = `id, user_id, amount::text, category, spent_at::text, comment,
       created_at, updated_at, version`

type ExpenseRepository struct{ pool *pgxpool.Pool }

func NewExpenseRepository(pool *pgxpool.Pool) *ExpenseRepository {
	return &ExpenseRepository{pool: pool}
}

func (r *ExpenseRepository) Create(ctx context.Context, input expense.NewExpense) (expense.Expense, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return expense.Expense{}, fmt.Errorf("begin create expense: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := ensureUser(ctx, tx, input.UserID); err != nil {
		return expense.Expense{}, err
	}
	row := tx.QueryRow(ctx, `INSERT INTO expenses (user_id, amount, category, spent_at, comment)
		VALUES ($1, $2::numeric, $3, $4::date, $5) RETURNING `+expenseColumns,
		input.UserID, input.Amount.String(), input.Category, input.SpentAt.String(), input.Comment)
	created, err := scanExpense(row)
	if err != nil {
		return expense.Expense{}, fmt.Errorf("insert expense: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return expense.Expense{}, fmt.Errorf("commit create expense: %w", err)
	}
	return created, nil
}

func (r *ExpenseRepository) Get(ctx context.Context, scope expense.Scope, id int64) (expense.Expense, error) {
	conditions, arguments := scopeConditions(scope, id)
	item, err := scanExpense(r.pool.QueryRow(ctx, `SELECT `+expenseColumns+` FROM expenses WHERE `+strings.Join(conditions, " AND "), arguments...))
	if errors.Is(err, pgx.ErrNoRows) {
		return expense.Expense{}, expense.ErrNotFound
	}
	if err != nil {
		return expense.Expense{}, fmt.Errorf("get expense: %w", err)
	}
	return item, nil
}

func (r *ExpenseRepository) List(ctx context.Context, filter expense.ListFilter) (expense.ListResult, error) {
	conditions, arguments := filterConditions(filter)
	where := strings.Join(conditions, " AND ")
	var total int64
	if err := r.pool.QueryRow(ctx, "SELECT count(*) FROM expenses WHERE "+where, arguments...).Scan(&total); err != nil {
		return expense.ListResult{}, fmt.Errorf("count expenses: %w", err)
	}
	listArguments := append(append([]any{}, arguments...), filter.Limit, filter.Offset)
	query := fmt.Sprintf(`SELECT %s FROM expenses WHERE %s ORDER BY spent_at DESC, id DESC LIMIT $%d OFFSET $%d`,
		expenseColumns, where, len(listArguments)-1, len(listArguments))
	rows, err := r.pool.Query(ctx, query, listArguments...)
	if err != nil {
		return expense.ListResult{}, fmt.Errorf("list expenses: %w", err)
	}
	defer rows.Close()
	items := make([]expense.Expense, 0)
	for rows.Next() {
		item, err := scanExpense(rows)
		if err != nil {
			return expense.ListResult{}, fmt.Errorf("scan expense: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return expense.ListResult{}, fmt.Errorf("iterate expenses: %w", err)
	}
	return expense.ListResult{Items: items, TotalCount: total, Limit: filter.Limit, Offset: filter.Offset}, nil
}

func (r *ExpenseRepository) Summary(ctx context.Context, filter expense.ListFilter) (expense.Summary, error) {
	conditions, arguments := filterConditions(filter)
	rows, err := r.pool.Query(ctx, `SELECT category, coalesce(sum(amount), 0)::text, count(*)
		FROM expenses WHERE `+strings.Join(conditions, " AND ")+` GROUP BY category ORDER BY category`, arguments...)
	if err != nil {
		return expense.Summary{}, fmt.Errorf("summarize expenses: %w", err)
	}
	defer rows.Close()
	result := expense.Summary{ByCategory: make([]expense.CategorySummary, 0)}
	var totalKopecks int64
	for rows.Next() {
		var category expense.Category
		var amountText string
		var count int64
		if err := rows.Scan(&category, &amountText, &count); err != nil {
			return expense.Summary{}, fmt.Errorf("scan expense summary: %w", err)
		}
		amount, err := expense.ParseMoneyOrZero(amountText)
		if err != nil {
			return expense.Summary{}, fmt.Errorf("parse expense summary amount: %w", err)
		}
		totalKopecks += amount.Kopecks()
		result.Count += count
		result.ByCategory = append(result.ByCategory, expense.CategorySummary{Category: category, Amount: amount, Count: count})
	}
	if err := rows.Err(); err != nil {
		return expense.Summary{}, fmt.Errorf("iterate expense summary: %w", err)
	}
	result.TotalAmount, err = expense.MoneyFromKopecks(totalKopecks)
	if err != nil {
		return expense.Summary{}, fmt.Errorf("calculate expense summary: %w", err)
	}
	return result, nil
}

func (r *ExpenseRepository) Update(ctx context.Context, scope expense.Scope, id int64, command expense.UpdateCommand) (expense.Expense, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return expense.Expense{}, fmt.Errorf("begin update expense: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if command.UserID.Set {
		if err := ensureUser(ctx, tx, command.UserID.Value); err != nil {
			return expense.Expense{}, err
		}
	}
	sets := make([]string, 0, 7)
	arguments := make([]any, 0, 9)
	addSet := func(column string, value any, cast string) {
		arguments = append(arguments, value)
		sets = append(sets, fmt.Sprintf("%s = $%d%s", column, len(arguments), cast))
	}
	if command.UserID.Set {
		addSet("user_id", command.UserID.Value, "")
	}
	if command.Amount.Set {
		addSet("amount", command.Amount.Value.String(), "::numeric")
	}
	if command.Category.Set {
		addSet("category", command.Category.Value, "")
	}
	if command.SpentAt.Set {
		addSet("spent_at", command.SpentAt.Value.String(), "::date")
	}
	if command.Comment.Set {
		addSet("comment", command.Comment.Value, "")
	}
	sets = append(sets, "updated_at = now()", "version = version + 1")
	arguments = append(arguments, id, command.ExpectedVersion)
	where := fmt.Sprintf("id = $%d AND version = $%d", len(arguments)-1, len(arguments))
	if scope.OwnerID != nil {
		arguments = append(arguments, *scope.OwnerID)
		where += fmt.Sprintf(" AND user_id = $%d", len(arguments))
	}
	updated, err := scanExpense(tx.QueryRow(ctx, `UPDATE expenses SET `+strings.Join(sets, ", ")+` WHERE `+where+` RETURNING `+expenseColumns, arguments...))
	if errors.Is(err, pgx.ErrNoRows) {
		return expense.Expense{}, classifyVersionMiss(ctx, tx, scope, id)
	}
	if err != nil {
		return expense.Expense{}, fmt.Errorf("update expense: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return expense.Expense{}, fmt.Errorf("commit update expense: %w", err)
	}
	return updated, nil
}

func (r *ExpenseRepository) Delete(ctx context.Context, scope expense.Scope, id, expectedVersion int64) error {
	arguments := []any{id, expectedVersion}
	where := "id = $1 AND version = $2"
	if scope.OwnerID != nil {
		arguments = append(arguments, *scope.OwnerID)
		where += " AND user_id = $3"
	}
	tag, err := r.pool.Exec(ctx, "DELETE FROM expenses WHERE "+where, arguments...)
	if err != nil {
		return fmt.Errorf("delete expense: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return classifyVersionMiss(ctx, r.pool, scope, id)
	}
	return nil
}

func (r *ExpenseRepository) ListUsers(ctx context.Context) ([]expense.User, error) {
	rows, err := r.pool.Query(ctx, "SELECT id, display_name, created_at FROM users ORDER BY coalesce(display_name, ''), id")
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	users := make([]expense.User, 0)
	for rows.Next() {
		var user expense.User
		var displayName pgtype.Text
		if err := rows.Scan(&user.ID, &displayName, &user.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		if displayName.Valid {
			user.DisplayName = &displayName.String
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func ensureUser(ctx context.Context, tx pgx.Tx, userID int64) error {
	if _, err := tx.Exec(ctx, "INSERT INTO users (id) VALUES ($1) ON CONFLICT (id) DO NOTHING", userID); err != nil {
		return fmt.Errorf("ensure expense owner: %w", err)
	}
	return nil
}

type scanner interface{ Scan(dest ...any) error }

func scanExpense(row scanner) (expense.Expense, error) {
	var item expense.Expense
	var amountText, dateText string
	var comment pgtype.Text
	if err := row.Scan(&item.ID, &item.UserID, &amountText, &item.Category, &dateText, &comment, &item.CreatedAt, &item.UpdatedAt, &item.Version); err != nil {
		return expense.Expense{}, err
	}
	amount, err := expense.ParseMoney(amountText)
	if err != nil {
		return expense.Expense{}, fmt.Errorf("parse stored amount: %w", err)
	}
	spentAt, err := expense.ParseDate(dateText)
	if err != nil {
		return expense.Expense{}, fmt.Errorf("parse stored date: %w", err)
	}
	item.Amount, item.SpentAt = amount, spentAt
	if comment.Valid {
		item.Comment = &comment.String
	}
	return item, nil
}

func filterConditions(filter expense.ListFilter) ([]string, []any) {
	conditions := []string{"TRUE"}
	arguments := make([]any, 0, 5)
	add := func(template string, value any) {
		arguments = append(arguments, value)
		conditions = append(conditions, fmt.Sprintf(template, len(arguments)))
	}
	if filter.UserID != nil {
		add("user_id = $%d", *filter.UserID)
	}
	if filter.Category != nil {
		add("category = $%d", *filter.Category)
	}
	if filter.DateFrom != nil {
		add("spent_at >= $%d::date", filter.DateFrom.String())
	}
	if filter.DateTo != nil {
		add("spent_at <= $%d::date", filter.DateTo.String())
	}
	if filter.Query != "" {
		add(`comment ILIKE '%%' || $%d || '%%' ESCAPE '\'`, escapeLike(filter.Query))
	}
	return conditions, arguments
}

func scopeConditions(scope expense.Scope, id int64) ([]string, []any) {
	conditions, arguments := []string{"id = $1"}, []any{id}
	if scope.OwnerID != nil {
		conditions, arguments = append(conditions, "user_id = $2"), append(arguments, *scope.OwnerID)
	}
	return conditions, arguments
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func classifyVersionMiss(ctx context.Context, querier rowQuerier, scope expense.Scope, id int64) error {
	conditions, arguments := scopeConditions(scope, id)
	var version int64
	err := querier.QueryRow(ctx, "SELECT version FROM expenses WHERE "+strings.Join(conditions, " AND "), arguments...).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return expense.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("classify expense version miss: %w", err)
	}
	return expense.ErrVersionConflict
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "%", `\%`)
	return strings.ReplaceAll(value, "_", `\_`)
}
