package httpapi

import (
	"context"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/shopspring/decimal"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/savik"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

func init() {
	// The API documents amounts as JSON numbers, while decimal defaults to strings.
	decimal.MarshalJSONWithoutQuotes = true
}

// LedgerService is the part of ledger.Service the API needs.
type LedgerService interface {
	Create(ctx context.Context, in ledger.CreateInput) (ledger.Operation, error)
	Get(ctx context.Context, s ledger.Scope, id int64) (ledger.Operation, error)
	Update(ctx context.Context, s ledger.Scope, id int64, p ledger.Patch) (ledger.Operation, error)
	Delete(ctx context.Context, s ledger.Scope, id int64) error
	List(ctx context.Context, s ledger.Scope, f ledger.Filter) (ledger.Page, error)
	Summary(ctx context.Context, s ledger.Scope, f ledger.Filter) (ledger.Summary, error)
	TimeSeries(ctx context.Context, s ledger.Scope, f ledger.Filter, b ledger.Bucket) ([]ledger.Point, error)
	TodayIn(loc *time.Location) time.Time
}

// FamilyService manages users, families and invites; *family.Service implements it.
type FamilyService interface {
	Admitter
	Consent(ctx context.Context, telegramID int64) (user.User, error)
	SetTimezone(ctx context.Context, telegramID int64, tz string) (user.User, error)
	SetWeeklyReport(ctx context.Context, by user.User, telegramID int64, on bool) (user.User, error)
	DeleteAccount(ctx context.Context, telegramID int64) error
	Members(ctx context.Context, familyID int64) ([]user.User, error)
	Remove(ctx context.Context, head user.User, telegramID int64) error
	Leave(ctx context.Context, telegramID int64) (user.User, error)
	TransferHead(ctx context.Context, head user.User, telegramID int64) error
	CreateInvite(ctx context.Context, head user.User) (family.CreatedInvite, error)
	ListInvites(ctx context.Context, familyID int64) ([]family.Invite, error)
	RevokeInvite(ctx context.Context, familyID, id int64) error
	Now() time.Time
}

// Server implements StrictServerInterface for the user of the request (see
// Auth): a family head reads the operations of the whole family, everyone
// writes only their own.
//
// Domain errors are returned as plain errors and turned into HTTP responses
// in one place (writeError), instead of building 400/404 objects in each handler.
type Server struct {
	ledger LedgerService
	family FamilyService
	loc    *time.Location // timezone of users who have not chosen one
}

var _ StrictServerInterface = (*Server)(nil)

func NewServer(l LedgerService, f FamilyService, loc *time.Location) *Server {
	return &Server{ledger: l, family: f, loc: loc}
}

// readScope is what the user may see: the head sees the whole family.
func readScope(u user.User) ledger.Scope {
	if u.IsHead {
		return ledger.InFamily(u.FamilyID)
	}
	return ledger.OwnedBy(u.TelegramID)
}

// writeScope is what the user may change: only their own operations, the
// head included — a family shares the view, not the pen.
func writeScope(u user.User) ledger.Scope { return ledger.OwnedBy(u.TelegramID) }

func (s *Server) today(u user.User) time.Time { return s.ledger.TodayIn(u.Location(s.loc)) }

// Me.

func (s *Server) GetMe(ctx context.Context, _ GetMeRequestObject) (GetMeResponseObject, error) {
	u, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	return GetMe200JSONResponse(s.toAPIMe(u)), nil
}

func (s *Server) UpdateMe(ctx context.Context, req UpdateMeRequestObject) (UpdateMeResponseObject, error) {
	u, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	if b.Consent != nil {
		if !*b.Consent {
			return nil, &ledger.ValidationError{Field: "consent", Message: "can only be accepted; delete the account to withdraw it"}
		}
		if u, err = s.family.Consent(ctx, u.TelegramID); err != nil {
			return nil, err
		}
	}
	// The rest is personal data: only after the policy is accepted.
	if (b.Timezone != nil || b.WeeklyReport != nil) && !u.Consented() {
		return nil, ErrConsentRequired
	}
	if b.Timezone != nil {
		if u, err = s.family.SetTimezone(ctx, u.TelegramID, *b.Timezone); err != nil {
			return nil, err
		}
	}
	if b.WeeklyReport != nil {
		if u, err = s.family.SetWeeklyReport(ctx, u, u.TelegramID, *b.WeeklyReport); err != nil {
			return nil, err
		}
	}
	return UpdateMe200JSONResponse(s.toAPIMe(u)), nil
}

func (s *Server) DeleteMe(ctx context.Context, _ DeleteMeRequestObject) (DeleteMeResponseObject, error) {
	u, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.family.DeleteAccount(ctx, u.TelegramID); err != nil {
		return nil, err
	}
	return DeleteMe204Response{}, nil
}

func (s *Server) toAPIMe(u user.User) Me {
	return Me{
		TelegramId:        u.TelegramID,
		FirstName:         u.FirstName,
		Username:          optional(u.Username),
		FamilyId:          u.FamilyID,
		IsHead:            u.IsHead,
		WeeklyReport:      u.WeeklyReport,
		Timezone:          optional(u.Timezone),
		EffectiveTimezone: u.Location(s.loc).String(),
		Consented:         u.Consented(),
		CreatedAt:         u.CreatedAt,
	}
}

// Operations.

func (s *Server) ListOperations(ctx context.Context, req ListOperationsRequestObject) (ListOperationsResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	p := req.Params
	f := filterFrom(p.UserId, p.Category, p.From, p.To)
	if p.Kind != nil {
		k := ledger.Kind(*p.Kind)
		f.Kind = &k
	}
	f.Query = deref(p.Q)
	f.Limit = deref(p.Limit)
	f.Offset = deref(p.Offset)

	page, err := s.ledger.List(ctx, readScope(u), f)
	if err != nil {
		return nil, err
	}
	items := make([]Operation, len(page.Items))
	for i, o := range page.Items {
		items[i] = toAPIOperation(o)
	}
	return ListOperations200JSONResponse{
		Items: items, Total: page.Total, ExpenseSum: page.ExpenseSum, IncomeSum: page.IncomeSum,
	}, nil
}

func (s *Server) CreateOperation(ctx context.Context, req CreateOperationRequestObject) (CreateOperationResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	o, err := s.ledger.Create(ctx, ledger.CreateInput{
		UserID:   u.TelegramID, // always the caller: nobody records for someone else
		Amount:   b.Amount,
		Category: ledger.Category(b.Category),
		Date:     dateTime(b.Date),
		Comment:  deref(b.Comment),
		Today:    s.today(u),
	})
	if err != nil {
		return nil, err
	}
	return CreateOperation201JSONResponse(toAPIOperation(o)), nil
}

func (s *Server) GetOperation(ctx context.Context, req GetOperationRequestObject) (GetOperationResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	o, err := s.ledger.Get(ctx, readScope(u), req.Id)
	if err != nil {
		return nil, err
	}
	return GetOperation200JSONResponse(toAPIOperation(o)), nil
}

func (s *Server) UpdateOperation(ctx context.Context, req UpdateOperationRequestObject) (UpdateOperationResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	p := ledger.Patch{Amount: b.Amount, Date: dateTime(b.Date), Comment: b.Comment, Today: s.today(u)}
	if b.Category != nil {
		c := ledger.Category(*b.Category)
		p.Category = &c
	}
	o, err := s.ledger.Update(ctx, writeScope(u), req.Id, p)
	if err != nil {
		return nil, err
	}
	return UpdateOperation200JSONResponse(toAPIOperation(o)), nil
}

func (s *Server) DeleteOperation(ctx context.Context, req DeleteOperationRequestObject) (DeleteOperationResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.ledger.Delete(ctx, writeScope(u), req.Id); err != nil {
		return nil, err
	}
	return DeleteOperation204Response{}, nil
}

func (s *Server) GetSummary(ctx context.Context, req GetSummaryRequestObject) (GetSummaryResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	p := req.Params
	sum, err := s.ledger.Summary(ctx, readScope(u), filterFrom(p.UserId, p.Category, p.From, p.To))
	if err != nil {
		return nil, err
	}
	return GetSummary200JSONResponse{
		Expense: toAPITotals(sum, ledger.KindExpense),
		Income:  toAPITotals(sum, ledger.KindIncome),
		Balance: sum.Balance(),
	}, nil
}

func (s *Server) GetTimeSeries(ctx context.Context, req GetTimeSeriesRequestObject) (GetTimeSeriesResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	p := req.Params
	f := filterFrom(p.UserId, p.Category, p.From, p.To)
	kind := ledger.Kind(p.Kind)
	if !kind.Valid() {
		return nil, &ledger.ValidationError{Field: "kind", Message: "must be expense or income"}
	}
	f.Kind = &kind
	points, err := s.ledger.TimeSeries(ctx, readScope(u), f, ledger.Bucket(p.Bucket))
	if err != nil {
		return nil, err
	}
	items := make([]TimePoint, len(points))
	for i, pt := range points {
		items[i] = TimePoint{Start: openapi_types.Date{Time: pt.Start}, Sum: pt.Sum, Count: pt.Count, Categories: toAPICategories(pt.Categories)}
	}
	return GetTimeSeries200JSONResponse{Bucket: p.Bucket, Kind: p.Kind, Items: items}, nil
}

func (s *Server) GetSavik(ctx context.Context, _ GetSavikRequestObject) (GetSavikResponseObject, error) {
	u, err := consented(ctx)
	if err != nil {
		return nil, err
	}
	// Savik reflects the user's own money, even for a family head.
	today := s.today(u)
	from := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	month, err := s.ledger.Summary(ctx, ledger.OwnedBy(u.TelegramID), ledger.Filter{From: &from, To: &today})
	if err != nil {
		return nil, err
	}
	return GetSavik200JSONResponse{
		State: SavikState(savik.Of(month)),
		Month: SavikMonth{
			From:    openapi_types.Date{Time: from},
			To:      openapi_types.Date{Time: today},
			Expense: month.Expense.Sum,
			Income:  month.Income.Sum,
			Balance: month.Balance(),
		},
	}, nil
}

func (s *Server) ListCategories(context.Context, ListCategoriesRequestObject) (ListCategoriesResponseObject, error) {
	all := append(append([]ledger.Category{}, ledger.ExpenseCategories...), ledger.IncomeCategories...)
	items := make([]CategoryInfo, len(all))
	for i, c := range all {
		items[i] = CategoryInfo{Code: Category(c), Kind: Kind(c.Kind()), Label: c.Label()}
	}
	return ListCategories200JSONResponse{Items: items}, nil
}

func toAPIOperation(o ledger.Operation) Operation {
	return Operation{
		Id:        o.ID,
		UserId:    o.UserID,
		Kind:      Kind(o.Kind()),
		Amount:    o.Amount,
		Category:  Category(o.Category),
		Date:      openapi_types.Date{Time: o.Date},
		Comment:   optional(o.Comment),
		CreatedAt: o.CreatedAt,
		UpdatedAt: o.UpdatedAt,
	}
}

func toAPITotals(s ledger.Summary, k ledger.Kind) Totals {
	t := s.Expense
	if k == ledger.KindIncome {
		t = s.Income
	}
	return Totals{Sum: t.Sum, Count: t.Count, Categories: toAPICategories(s.Of(k))}
}

func toAPICategories(totals []ledger.CategoryTotal) []CategoryTotal {
	out := make([]CategoryTotal, len(totals))
	for i, t := range totals {
		out[i] = CategoryTotal{Category: Category(t.Category), Sum: t.Sum, Count: t.Count}
	}
	return out
}

func filterFrom(userID *int64, category *Category, from, to *openapi_types.Date) ledger.Filter {
	f := ledger.Filter{UserID: userID, From: dateTime(from), To: dateTime(to)}
	if category != nil {
		c := ledger.Category(*category)
		f.Category = &c
	}
	return f
}

