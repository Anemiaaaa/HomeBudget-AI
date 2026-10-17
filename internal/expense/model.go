package expense

import (
	"context"
	"errors"
	"time"
)

type Category string

const (
	CategoryFood          Category = "food"
	CategoryTransport     Category = "transport"
	CategoryHousing       Category = "housing"
	CategoryEntertainment Category = "entertainment"
	CategoryHealth        Category = "health"
	CategoryShopping      Category = "shopping"
	CategoryOther         Category = "other"
)

var Categories = []Category{
	CategoryFood, CategoryTransport, CategoryHousing, CategoryEntertainment,
	CategoryHealth, CategoryShopping, CategoryOther,
}

var (
	ErrNotFound        = errors.New("expense not found")
	ErrVersionConflict = errors.New("expense version conflict")
)

type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string { return "expense validation failed" }

type Optional[T any] struct {
	Set   bool
	Value T
}

type Expense struct {
	ID        int64
	UserID    int64
	Amount    Money
	Category  Category
	SpentAt   Date
	Comment   *string
	CreatedAt time.Time
	UpdatedAt time.Time
	Version   int64
}

type User struct {
	ID          int64
	DisplayName *string
	CreatedAt   time.Time
}

type CreateInput struct {
	UserID   int64
	Amount   string
	Category Category
	SpentAt  string
	Comment  *string
}

type CreateFields struct {
	Amount   string
	Category Category
	SpentAt  string
	Comment  *string
}

type NewExpense struct {
	UserID   int64
	Amount   Money
	Category Category
	SpentAt  Date
	Comment  *string
}

type UpdateInput struct {
	ExpectedVersion int64
	UserID          Optional[int64]
	Amount          Optional[string]
	Category        Optional[Category]
	SpentAt         Optional[string]
	Comment         Optional[*string]
}

type UpdateCommand struct {
	ExpectedVersion int64
	UserID          Optional[int64]
	Amount          Optional[Money]
	Category        Optional[Category]
	SpentAt         Optional[Date]
	Comment         Optional[*string]
}

type Scope struct {
	OwnerID *int64
}

type ListOrder string

const (
	OrderSpentAtDesc   ListOrder = "spent_at_desc"
	OrderCreatedAtDesc ListOrder = "created_at_desc"
)

type ListFilter struct {
	UserID   *int64
	Category *Category
	DateFrom *Date
	DateTo   *Date
	Query    string
	Order    ListOrder
	Limit    int
	Offset   int
}

type ListResult struct {
	Items      []Expense
	TotalCount int64
	Limit      int
	Offset     int
}

type CategorySummary struct {
	Category Category
	Amount   Money
	Count    int64
}

type Summary struct {
	TotalAmount Money
	Count       int64
	ByCategory  []CategorySummary
}

type Repository interface {
	Create(context.Context, NewExpense) (Expense, error)
	CreateMany(context.Context, []NewExpense) ([]Expense, error)
	Get(context.Context, Scope, int64) (Expense, error)
	List(context.Context, ListFilter) (ListResult, error)
	Summary(context.Context, ListFilter) (Summary, error)
	Update(context.Context, Scope, int64, UpdateCommand) (Expense, error)
	Delete(context.Context, Scope, int64, int64) error
	ListUsers(context.Context) ([]User, error)
}
