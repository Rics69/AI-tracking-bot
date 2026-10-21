package httpapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

func init() {
	// The API documents amounts as JSON numbers, while decimal defaults to strings.
	decimal.MarshalJSONWithoutQuotes = true
}

// ExpenseService is the part of ledger.Service the API needs.
type ExpenseService interface {
	Create(ctx context.Context, in ledger.CreateInput) (ledger.Operation, error)
	Get(ctx context.Context, s ledger.Scope, id int64) (ledger.Operation, error)
	Update(ctx context.Context, s ledger.Scope, id int64, p ledger.Patch) (ledger.Operation, error)
	Delete(ctx context.Context, s ledger.Scope, id int64) error
	List(ctx context.Context, s ledger.Scope, f ledger.Filter) (ledger.Page, error)
	Summary(ctx context.Context, s ledger.Scope, f ledger.Filter) (ledger.Summary, error)
	TimeSeries(ctx context.Context, s ledger.Scope, f ledger.Filter, b ledger.Bucket) ([]ledger.Point, error)
}

// FamilyService manages members and invites; *family.Service implements it.
type FamilyService interface {
	Member(ctx context.Context, familyID, telegramID int64) (user.User, error)
	Members(ctx context.Context, familyID int64) ([]user.User, error)
	Remove(ctx context.Context, head user.User, telegramID int64) error
	SetWeeklyReport(ctx context.Context, by user.User, telegramID int64, on bool) (user.User, error)
	CreateInvite(ctx context.Context, head user.User) (family.CreatedInvite, error)
	ListInvites(ctx context.Context, familyID int64) ([]family.Invite, error)
	RevokeInvite(ctx context.Context, familyID, id int64) error
	Now() time.Time
}

// Viewer returns the user the panel works for.
type Viewer func(ctx context.Context) (user.User, error)

// ErrNoViewer means the panel does not know whose family to show.
var ErrNoViewer = errors.New("the panel has no user: set ADMIN_TELEGRAM_ID and write to the bot first")

// Server implements StrictServerInterface. The panel belongs to a family head,
// so every operation works in the scope of the head's family.
//
// Domain errors are returned as plain errors and turned into HTTP responses
// in one place (writeError), instead of building 400/404 objects in each handler.
type Server struct {
	expenses ExpenseService
	family   FamilyService
	viewer   Viewer
}

var _ StrictServerInterface = (*Server)(nil)

func NewServer(expenses ExpenseService, family FamilyService, viewer Viewer) *Server {
	return &Server{expenses: expenses, family: family, viewer: viewer}
}

// head returns the viewer, who must be the head of their family.
func (s *Server) head(ctx context.Context) (user.User, error) {
	u, err := s.viewer(ctx)
	if err != nil {
		return user.User{}, err
	}
	if !u.IsHead {
		return user.User{}, family.ErrNotHead
	}
	return u, nil
}

// scope is the head's family: what the panel may see and change.
func (s *Server) scope(ctx context.Context) (ledger.Scope, user.User, error) {
	u, err := s.head(ctx)
	if err != nil {
		return ledger.Scope{}, user.User{}, err
	}
	return ledger.InFamily(u.FamilyID), u, nil
}

// member checks that an expense owner chosen in the panel is in the family.
func (s *Server) member(ctx context.Context, head user.User, telegramID int64) error {
	_, err := s.family.Member(ctx, head.FamilyID, telegramID)
	if errors.Is(err, family.ErrNotMember) {
		return ledger.ErrUnknownUser
	}
	return err
}

func (s *Server) ListExpenses(ctx context.Context, req ListExpensesRequestObject) (ListExpensesResponseObject, error) {
	p := req.Params
	f := filterFrom(p.UserId, p.Category, p.From, p.To)
	f.Query = deref(p.Q)
	f.Limit = deref(p.Limit)
	f.Offset = deref(p.Offset)

	scope, _, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	page, err := s.expenses.List(ctx, scope, f)
	if err != nil {
		return nil, err
	}
	items := make([]Expense, len(page.Items))
	for i, e := range page.Items {
		items[i] = toAPIExpense(e)
	}
	return ListExpenses200JSONResponse{Items: items, Total: page.Total, Sum: page.ExpenseSum}, nil
}

func (s *Server) CreateExpense(ctx context.Context, req CreateExpenseRequestObject) (CreateExpenseResponseObject, error) {
	_, head, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	if err := s.member(ctx, head, b.UserId); err != nil {
		return nil, err
	}
	if err := expenseCategory(ledger.Category(b.Category)); err != nil {
		return nil, err
	}
	in := ledger.CreateInput{
		UserID:   b.UserId,
		Amount:   b.Amount,
		Category: ledger.Category(b.Category),
		Date:     dateTime(b.SpentAt),
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
	scope, _, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	sum, err := s.expenses.Summary(ctx, scope, filterFrom(p.UserId, p.Category, p.From, p.To))
	if err != nil {
		return nil, err
	}
	spent := sum.Of(ledger.KindExpense)
	items := make([]CategoryTotal, len(spent))
	for i, t := range spent {
		items[i] = CategoryTotal{Category: Category(t.Category), Sum: t.Sum, Count: t.Count}
	}
	return GetExpenseSummary200JSONResponse{Items: items, Sum: sum.Expense.Sum, Count: sum.Expense.Count}, nil
}

func (s *Server) GetExpenseTimeSeries(ctx context.Context, req GetExpenseTimeSeriesRequestObject) (GetExpenseTimeSeriesResponseObject, error) {
	p := req.Params
	scope, _, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	points, err := s.expenses.TimeSeries(ctx, scope,
		filterFrom(p.UserId, p.Category, p.From, p.To), ledger.Bucket(p.Bucket))
	if err != nil {
		return nil, err
	}
	items := make([]TimePoint, len(points))
	for i, pt := range points {
		cats := make([]CategoryTotal, len(pt.Categories))
		for j, c := range pt.Categories {
			cats[j] = CategoryTotal{Category: Category(c.Category), Sum: c.Sum, Count: c.Count}
		}
		items[i] = TimePoint{Start: openapi_types.Date{Time: pt.Start}, Sum: pt.Sum, Count: pt.Count, Categories: cats}
	}
	return GetExpenseTimeSeries200JSONResponse{Bucket: p.Bucket, Items: items}, nil
}

func (s *Server) GetExpense(ctx context.Context, req GetExpenseRequestObject) (GetExpenseResponseObject, error) {
	scope, _, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	e, err := s.getExpense(ctx, scope, req.Id)
	if err != nil {
		return nil, err
	}
	return GetExpense200JSONResponse(toAPIExpense(e)), nil
}

func (s *Server) UpdateExpense(ctx context.Context, req UpdateExpenseRequestObject) (UpdateExpenseResponseObject, error) {
	scope, head, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	if b.UserId != nil {
		if err := s.member(ctx, head, *b.UserId); err != nil {
			return nil, err
		}
	}
	p := ledger.Patch{
		UserID:  b.UserId,
		Amount:  b.Amount,
		Date:    dateTime(b.SpentAt),
		Comment: b.Comment,
	}
	if b.Category != nil {
		c := ledger.Category(*b.Category)
		if err := expenseCategory(c); err != nil {
			return nil, err
		}
		p.Category = &c
	}
	if _, err := s.getExpense(ctx, scope, req.Id); err != nil {
		return nil, err
	}
	e, err := s.expenses.Update(ctx, scope, req.Id, p)
	if err != nil {
		return nil, err
	}
	return UpdateExpense200JSONResponse(toAPIExpense(e)), nil
}

func (s *Server) DeleteExpense(ctx context.Context, req DeleteExpenseRequestObject) (DeleteExpenseResponseObject, error) {
	scope, _, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.getExpense(ctx, scope, req.Id); err != nil {
		return nil, err
	}
	if err := s.expenses.Delete(ctx, scope, req.Id); err != nil {
		return nil, err
	}
	return DeleteExpense204Response{}, nil
}

// getExpense returns an operation only if it is an expense: incomes are not
// part of this API yet, so for it they do not exist.
func (s *Server) getExpense(ctx context.Context, scope ledger.Scope, id int64) (ledger.Operation, error) {
	e, err := s.expenses.Get(ctx, scope, id)
	if err != nil {
		return ledger.Operation{}, err
	}
	if e.Kind() != ledger.KindExpense {
		return ledger.Operation{}, ledger.ErrNotFound
	}
	return e, nil
}

func expenseCategory(c ledger.Category) error {
	if !c.Valid() || c.Kind() != ledger.KindExpense {
		return &ledger.ValidationError{Field: "category", Message: fmt.Sprintf("unknown expense category %q", c)}
	}
	return nil
}

func (s *Server) ListUsers(ctx context.Context, _ ListUsersRequestObject) (ListUsersResponseObject, error) {
	head, err := s.head(ctx)
	if err != nil {
		return nil, err
	}
	users, err := s.family.Members(ctx, head.FamilyID)
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
	items := make([]CategoryInfo, len(ledger.ExpenseCategories))
	for i, c := range ledger.ExpenseCategories {
		items[i] = CategoryInfo{Code: Category(c), Label: c.Label()}
	}
	return ListCategories200JSONResponse{Items: items}, nil
}

func toAPIExpense(e ledger.Operation) Expense {
	return Expense{
		Id:        e.ID,
		UserId:    e.UserID,
		Amount:    e.Amount,
		Category:  Category(e.Category),
		SpentAt:   openapi_types.Date{Time: e.Date},
		Comment:   optional(e.Comment),
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}
}

// filterFrom builds a filter of expenses: the API does not serve incomes yet.
func filterFrom(userID *int64, category *Category, from, to *openapi_types.Date) ledger.Filter {
	expense := ledger.KindExpense
	f := ledger.Filter{UserID: userID, Kind: &expense, From: dateTime(from), To: dateTime(to)}
	if category != nil {
		c := ledger.Category(*category)
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
		CreatedAt:    u.CreatedAt,
	}
}
