package ledger

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// memRepo is a minimal in-memory Repository for service tests.
type memRepo struct {
	items  map[int64]Operation
	nextID int64
	// lastFilter records what the service passed to List/Summary.
	lastFilter Filter
}

func newMemRepo() *memRepo { return &memRepo{items: map[int64]Operation{}} }

// Every user of memRepo belongs to family 1.
func (r *memRepo) visible(s Scope, e Operation) bool {
	return s.FamilyID() == 1 || e.UserID == s.UserID()
}

var family = InFamily(1)

func (r *memRepo) Create(_ context.Context, e Operation) (Operation, error) {
	r.nextID++
	e.ID = r.nextID
	r.items[e.ID] = e
	return e, nil
}

func (r *memRepo) Get(_ context.Context, s Scope, id int64) (Operation, error) {
	e, ok := r.items[id]
	if !ok || !r.visible(s, e) {
		return Operation{}, ErrNotFound
	}
	return e, nil
}

func (r *memRepo) Update(ctx context.Context, s Scope, e Operation) (Operation, error) {
	if _, err := r.Get(ctx, s, e.ID); err != nil {
		return Operation{}, err
	}
	r.items[e.ID] = e
	return e, nil
}

func (r *memRepo) Delete(ctx context.Context, s Scope, id int64) error {
	if _, err := r.Get(ctx, s, id); err != nil {
		return err
	}
	delete(r.items, id)
	return nil
}

func (r *memRepo) List(_ context.Context, _ Scope, f Filter) (Page, error) {
	r.lastFilter = f
	return Page{}, nil
}

func (r *memRepo) Summary(_ context.Context, _ Scope, f Filter) (Summary, error) {
	r.lastFilter = f
	return Summary{}, nil
}

// aggregates is returned by Aggregate for time series tests.
var aggregates []BucketTotal

func (r *memRepo) Aggregate(context.Context, Scope, Filter, Bucket) ([]BucketTotal, error) {
	return aggregates, nil
}

var moscow = mustLoad("Europe/Moscow")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// newTestService returns a service whose clock is fixed at 2026-10-15 01:30 MSK,
// which is still 2026-10-14 in UTC: "today" must follow the family timezone.
func newTestService() (*Service, *memRepo) {
	repo := newMemRepo()
	svc := NewService(repo, moscow)
	svc.now = func() time.Time { return time.Date(2026, 10, 14, 22, 30, 0, 0, time.UTC) }
	return svc, repo
}

func date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func validInput() CreateInput {
	return CreateInput{UserID: 1, Amount: decimal.RequireFromString("350"), Category: CategoryFood, Comment: "кофе"}
}

func TestCreateDefaultsToTodayInFamilyTimezone(t *testing.T) {
	svc, _ := newTestService()
	e, err := svc.Create(context.Background(), validInput())
	if err != nil {
		t.Fatal(err)
	}
	if want := date(2026, 10, 15); !e.Date.Equal(want) {
		t.Errorf("Date = %s, want %s", e.Date, want)
	}
}

func TestFutureIsJudgedByTheCallersToday(t *testing.T) {
	svc, _ := newTestService() // 15 October in Moscow, still 14 October in UTC
	in := validInput()
	in.Date = new(date(2026, 10, 15))
	in.Today = date(2026, 10, 14) // a user to the west of Moscow
	var verr *ValidationError
	if _, err := svc.Create(context.Background(), in); !errors.As(err, &verr) || verr.Field != "date" {
		t.Errorf("err = %v, want spent_at in the future for this user", err)
	}
	in.Today = date(2026, 10, 15)
	if _, err := svc.Create(context.Background(), in); err != nil {
		t.Errorf("today for the user: %v", err)
	}
	if got := svc.TodayIn(time.UTC); !got.Equal(date(2026, 10, 14)) {
		t.Errorf("TodayIn(UTC) = %s", got)
	}
}

func TestCreateValidation(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*CreateInput)
		field  string
	}{
		{"zero amount", func(in *CreateInput) { in.Amount = decimal.Zero }, "amount"},
		{"negative amount", func(in *CreateInput) { in.Amount = decimal.RequireFromString("-1") }, "amount"},
		{"three decimals", func(in *CreateInput) { in.Amount = decimal.RequireFromString("1.005") }, "amount"},
		{"too large", func(in *CreateInput) { in.Amount = decimal.New(1, 10) }, "amount"},
		{"unknown category", func(in *CreateInput) { in.Category = "coffee" }, "category"},
		{"future date", func(in *CreateInput) { in.Date = new(date(2026, 10, 16)) }, "date"},
		{"ancient date", func(in *CreateInput) { in.Date = new(date(1999, 12, 31)) }, "date"},
		{"long comment", func(in *CreateInput) { in.Comment = strings.Repeat("я", MaxCommentLength+1) }, "comment"},
		{"no user", func(in *CreateInput) { in.UserID = 0 }, "user_id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newTestService()
			in := validInput()
			tt.modify(&in)
			_, err := svc.Create(context.Background(), in)
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("err = %v, want ValidationError", err)
			}
			if verr.Field != tt.field {
				t.Errorf("field = %q, want %q", verr.Field, tt.field)
			}
		})
	}
}

func TestCreateNormalizes(t *testing.T) {
	svc, _ := newTestService()
	in := validInput()
	in.Comment = "  кофе  "
	in.Amount = decimal.RequireFromString("99.90")
	in.Date = new(time.Date(2026, 10, 10, 23, 59, 0, 0, time.UTC))
	e, err := svc.Create(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if e.Comment != "кофе" {
		t.Errorf("Comment = %q, want trimmed", e.Comment)
	}
	if !e.Date.Equal(date(2026, 10, 10)) {
		t.Errorf("Date = %s, want the date without time", e.Date)
	}
}

func TestUpdateAppliesPatch(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()
	e, _ := svc.Create(ctx, validInput())

	updated, err := svc.Update(ctx, OwnedBy(1), e.ID, Patch{Amount: new(decimal.RequireFromString("450")), Comment: new("")})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Amount.Equal(decimal.RequireFromString("450")) {
		t.Errorf("Amount = %s, want 450", updated.Amount)
	}
	if updated.Comment != "" {
		t.Errorf("Comment = %q, want cleared", updated.Comment)
	}
	if updated.Category != CategoryFood {
		t.Errorf("Category changed to %q", updated.Category)
	}
}

func TestUpdateValidatesResult(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()
	e, _ := svc.Create(ctx, validInput())

	_, err := svc.Update(ctx, family, e.ID, Patch{Amount: new(decimal.Zero)})
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
}

func TestMemberCannotTouchOthersExpenses(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()
	e, _ := svc.Create(ctx, validInput()) // owned by user 1

	if _, err := svc.Get(ctx, OwnedBy(2), e.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get by another user: err = %v, want ErrNotFound", err)
	}
	if _, err := svc.Update(ctx, OwnedBy(2), e.ID, Patch{Amount: new(decimal.NewFromInt(1))}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update by another user: err = %v, want ErrNotFound", err)
	}
	if err := svc.Delete(ctx, OwnedBy(2), e.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete by another user: err = %v, want ErrNotFound", err)
	}
	if _, err := svc.Get(ctx, Scope{}, e.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get with zero scope: err = %v, want ErrNotFound", err)
	}
}

func TestMemberCannotReassignExpense(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()
	e, _ := svc.Create(ctx, validInput())

	_, err := svc.Update(ctx, OwnedBy(1), e.ID, Patch{UserID: new(int64(2))})
	var verr *ValidationError
	if !errors.As(err, &verr) || verr.Field != "user_id" {
		t.Fatalf("err = %v, want user_id ValidationError", err)
	}
	// The family head can.
	if _, err := svc.Update(ctx, family, e.ID, Patch{UserID: new(int64(2))}); err != nil {
		t.Fatalf("family head reassign: %v", err)
	}
}

func TestListNormalizesFilter(t *testing.T) {
	svc, repo := newTestService()
	ctx := context.Background()

	if _, err := svc.List(ctx, family, Filter{}); err != nil {
		t.Fatal(err)
	}
	if repo.lastFilter.Limit != DefaultLimit {
		t.Errorf("default Limit = %d, want %d", repo.lastFilter.Limit, DefaultLimit)
	}

	if _, err := svc.List(ctx, family, Filter{Limit: 10_000, Query: "  такси "}); err != nil {
		t.Fatal(err)
	}
	if repo.lastFilter.Limit != MaxLimit {
		t.Errorf("Limit = %d, want capped at %d", repo.lastFilter.Limit, MaxLimit)
	}
	if repo.lastFilter.Query != "такси" {
		t.Errorf("Query = %q, want trimmed", repo.lastFilter.Query)
	}

	_, err := svc.List(ctx, family, Filter{From: new(date(2026, 10, 10)), To: new(date(2026, 10, 1))})
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Errorf("from > to: err = %v, want ValidationError", err)
	}
}

func TestTimeSeriesFillsGaps(t *testing.T) {
	svc, _ := newTestService() // today is 2026-10-15
	aggregates = []BucketTotal{
		{Start: date(2026, 10, 12), Category: CategoryFood, Sum: decimal.NewFromInt(300), Count: 2},
		{Start: date(2026, 10, 12), Category: CategoryHousing, Sum: decimal.NewFromInt(900), Count: 1},
		{Start: date(2026, 10, 14), Category: CategoryFood, Sum: decimal.NewFromInt(50), Count: 1},
	}
	defer func() { aggregates = nil }()

	points, err := svc.TimeSeries(context.Background(), family, Filter{}, BucketDay)
	if err != nil {
		t.Fatal(err)
	}
	// From the first expense to today, one point per day, empty days included.
	if len(points) != 4 || !points[0].Start.Equal(date(2026, 10, 12)) || !points[3].Start.Equal(date(2026, 10, 15)) {
		t.Fatalf("points = %+v", points)
	}
	first := points[0]
	if !first.Sum.Equal(decimal.NewFromInt(1200)) || first.Count != 3 || first.Categories[0].Category != CategoryHousing {
		t.Errorf("first point = %+v", first)
	}
	if !points[1].Sum.IsZero() || len(points[1].Categories) != 0 {
		t.Errorf("empty day = %+v", points[1])
	}
}

func TestTimeSeriesValidation(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()
	var verr *ValidationError
	if _, err := svc.TimeSeries(ctx, family, Filter{}, "year"); !errors.As(err, &verr) {
		t.Errorf("unknown bucket: err = %v", err)
	}
	if _, err := svc.TimeSeries(ctx, family, Filter{From: new(date(2020, 1, 1))}, BucketDay); !errors.As(err, &verr) {
		t.Errorf("6 years by day: err = %v, want too many points", err)
	}
	if points, err := svc.TimeSeries(ctx, family, Filter{}, BucketWeek); err != nil || len(points) != 0 {
		t.Errorf("no expenses, no period: %v %v", points, err)
	}
}

func TestTruncateDate(t *testing.T) {
	d := date(2026, 10, 18) // Sunday
	for b, want := range map[Bucket]time.Time{
		BucketDay: d, BucketWeek: date(2026, 10, 12), BucketMonth: date(2026, 10, 1),
	} {
		if got := TruncateDate(d, b); !got.Equal(want) {
			t.Errorf("TruncateDate(%s) = %s, want %s", b, got, want)
		}
	}
}
