package httpapi

import (
	"context"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/expense"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

func init() {
	// The API documents amounts as JSON numbers, while decimal defaults to strings.
	decimal.MarshalJSONWithoutQuotes = true
}

// ExpenseService is the part of expense.Service the API needs.
type ExpenseService interface {
	Create(ctx context.Context, in expense.CreateInput) (expense.Expense, error)
	Get(ctx context.Context, s expense.Scope, id int64) (expense.Expense, error)
	Update(ctx context.Context, s expense.Scope, id int64, p expense.Patch) (expense.Expense, error)
	Delete(ctx context.Context, s expense.Scope, id int64) error
	List(ctx context.Context, s expense.Scope, f expense.Filter) (expense.Page, error)
	Summary(ctx context.Context, s expense.Scope, f expense.Filter) (expense.Summary, error)
}

// FamilyService manages members and invites; *family.Service implements it.
type FamilyService interface {
	Members(ctx context.Context) ([]user.User, error)
	Remove(ctx context.Context, telegramID int64) error
	SetWeeklyReport(ctx context.Context, telegramID int64, on bool) (user.User, error)
	CreateInvite(ctx context.Context) (family.CreatedInvite, error)
	ListInvites(ctx context.Context) ([]family.Invite, error)
	RevokeInvite(ctx context.Context, id int64) error
	Now() time.Time
}

// Server implements StrictServerInterface. The panel belongs to the family head,
// so every operation works in the AllUsers scope.
//
// Domain errors are returned as plain errors and turned into HTTP responses
// in one place (writeError), instead of building 400/404 objects in each handler.
type Server struct {
	expenses ExpenseService
	family   FamilyService
}

var _ StrictServerInterface = (*Server)(nil)

func NewServer(expenses ExpenseService, family FamilyService) *Server {
	return &Server{expenses: expenses, family: family}
}

func (s *Server) ListExpenses(ctx context.Context, req ListExpensesRequestObject) (ListExpensesResponseObject, error) {
	p := req.Params
	f := filterFrom(p.UserId, p.Category, p.From, p.To)
	f.Query = deref(p.Q)
	f.Limit = deref(p.Limit)
	f.Offset = deref(p.Offset)

	page, err := s.expenses.List(ctx, expense.AllUsers(), f)
	if err != nil {
		return nil, err
	}
	items := make([]Expense, len(page.Items))
	for i, e := range page.Items {
		items[i] = toAPIExpense(e)
	}
	return ListExpenses200JSONResponse{Items: items, Total: page.Total, Sum: page.Sum}, nil
}

func (s *Server) CreateExpense(ctx context.Context, req CreateExpenseRequestObject) (CreateExpenseResponseObject, error) {
	b := req.Body
	in := expense.CreateInput{
		UserID:   b.UserId,
		Amount:   b.Amount,
		Category: expense.Category(b.Category),
		SpentAt:  dateTime(b.SpentAt),
		Comment:  deref(b.Comment),
	}
	e, err := s.expenses.Create(ctx, in)
	if err != nil {
		return nil, err
	}
	return CreateExpense201JSONResponse(toAPIExpense(e)), nil
}

func (s *Server) GetExpenseSummary(ctx context.Context, req GetExpenseSummaryRequestObject) (GetExpenseSummaryResponseObject, error) {
	p := req.Params
	sum, err := s.expenses.Summary(ctx, expense.AllUsers(), filterFrom(p.UserId, p.Category, p.From, p.To))
	if err != nil {
		return nil, err
	}
	items := make([]CategoryTotal, len(sum.Items))
	for i, t := range sum.Items {
		items[i] = CategoryTotal{Category: Category(t.Category), Sum: t.Sum, Count: t.Count}
	}
	return GetExpenseSummary200JSONResponse{Items: items, Sum: sum.Sum, Count: sum.Count}, nil
}

func (s *Server) GetExpense(ctx context.Context, req GetExpenseRequestObject) (GetExpenseResponseObject, error) {
	e, err := s.expenses.Get(ctx, expense.AllUsers(), req.Id)
	if err != nil {
		return nil, err
	}
	return GetExpense200JSONResponse(toAPIExpense(e)), nil
}

func (s *Server) UpdateExpense(ctx context.Context, req UpdateExpenseRequestObject) (UpdateExpenseResponseObject, error) {
	b := req.Body
	p := expense.Patch{
		UserID:  b.UserId,
		Amount:  b.Amount,
		SpentAt: dateTime(b.SpentAt),
		Comment: b.Comment,
	}
	if b.Category != nil {
		c := expense.Category(*b.Category)
		p.Category = &c
	}
	e, err := s.expenses.Update(ctx, expense.AllUsers(), req.Id, p)
	if err != nil {
		return nil, err
	}
	return UpdateExpense200JSONResponse(toAPIExpense(e)), nil
}

func (s *Server) DeleteExpense(ctx context.Context, req DeleteExpenseRequestObject) (DeleteExpenseResponseObject, error) {
	if err := s.expenses.Delete(ctx, expense.AllUsers(), req.Id); err != nil {
		return nil, err
	}
	return DeleteExpense204Response{}, nil
}

func (s *Server) ListUsers(ctx context.Context, _ ListUsersRequestObject) (ListUsersResponseObject, error) {
	users, err := s.family.Members(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]User, len(users))
	for i, u := range users {
		items[i] = toAPIUser(u)
	}
	return ListUsers200JSONResponse{Items: items}, nil
}

func (s *Server) ListCategories(context.Context, ListCategoriesRequestObject) (ListCategoriesResponseObject, error) {
	items := make([]CategoryInfo, len(expense.Categories))
	for i, c := range expense.Categories {
		items[i] = CategoryInfo{Code: Category(c), Label: c.Label()}
	}
	return ListCategories200JSONResponse{Items: items}, nil
}

func toAPIExpense(e expense.Expense) Expense {
	return Expense{
		Id:        e.ID,
		UserId:    e.UserID,
		Amount:    e.Amount,
		Category:  Category(e.Category),
		SpentAt:   openapi_types.Date{Time: e.SpentAt},
		Comment:   optional(e.Comment),
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}
}

func filterFrom(userID *int64, category *Category, from, to *openapi_types.Date) expense.Filter {
	f := expense.Filter{UserID: userID, From: dateTime(from), To: dateTime(to)}
	if category != nil {
		c := expense.Category(*category)
		f.Category = &c
	}
	return f
}

func toAPIUser(u user.User) User {
	return User{
		TelegramId:   u.TelegramID,
		FirstName:    u.FirstName,
		Username:     optional(u.Username),
		IsHead:       u.IsHead,
		WeeklyReport: u.WeeklyReport,
		RemovedAt:    u.RemovedAt,
		CreatedAt:    u.CreatedAt,
	}
}
