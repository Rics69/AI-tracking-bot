package ledger

import (
	"context"
	"slices"
	"time"

	"github.com/shopspring/decimal"
)

// Bucket is the step of a time series.
type Bucket string

const (
	BucketDay   Bucket = "day"
	BucketWeek  Bucket = "week" // weeks start on Monday
	BucketMonth Bucket = "month"
)

// maxPoints keeps a chart readable and the response small.
const maxPoints = 400

// BucketTotal is spending in one category within one bucket, as aggregated by the repository.
type BucketTotal struct {
	Start    time.Time // first day of the bucket
	Category Category
	Sum      decimal.Decimal
	Count    int
}

// Point is one bucket of a time series.
type Point struct {
	Start      time.Time
	Sum        decimal.Decimal
	Count      int
	Categories []CategoryTotal // largest first; empty for a bucket without operations
}

// TimeSeries returns spending per bucket for the filter. Buckets without
// operations are present with zero sums, so a chart shows gaps as gaps.
// Without From the series starts at the earliest matching operation, without To it ends today.
func (s *Service) TimeSeries(ctx context.Context, sc Scope, f Filter, b Bucket) ([]Point, error) {
	if b != BucketDay && b != BucketWeek && b != BucketMonth {
		return nil, invalid("bucket", "must be day, week or month")
	}
	if err := normalizeFilter(&f); err != nil {
		return nil, err
	}
	rows, err := s.repo.Aggregate(ctx, sc, f, b)
	if err != nil {
		return nil, err
	}

	var from, to time.Time
	switch {
	case f.From != nil:
		from = *f.From
	case len(rows) > 0:
		from = slices.MinFunc(rows, func(a, b BucketTotal) int { return a.Start.Compare(b.Start) }).Start
	default:
		return []Point{}, nil // no period and no operations: nothing to draw
	}
	to = s.Today()
	if f.To != nil {
		to = *f.To
	}
	from, to = TruncateDate(from, b), TruncateDate(to, b)
	if n := countBuckets(from, to, b); n > maxPoints {
		return nil, invalid("bucket", "too many points (%d) for the period: use a larger bucket", n)
	}

	byStart := map[time.Time][]BucketTotal{}
	for _, r := range rows {
		byStart[r.Start] = append(byStart[r.Start], r)
	}
	var points []Point
	for start := from; !start.After(to); start = next(start, b) {
		p := Point{Start: start, Sum: decimal.Zero, Categories: []CategoryTotal{}}
		for _, r := range byStart[start] {
			p.Sum = p.Sum.Add(r.Sum)
			p.Count += r.Count
			p.Categories = append(p.Categories, CategoryTotal{Category: r.Category, Sum: r.Sum, Count: r.Count})
		}
		slices.SortFunc(p.Categories, func(a, b CategoryTotal) int { return b.Sum.Cmp(a.Sum) })
		points = append(points, p)
	}
	return points, nil
}

// TruncateDate returns the first day of the bucket containing d, matching
// PostgreSQL's date_trunc (ISO weeks start on Monday).
func TruncateDate(d time.Time, b Bucket) time.Time {
	d = dateOf(d)
	switch b {
	case BucketWeek:
		return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
	case BucketMonth:
		return time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return d
	}
}

func next(d time.Time, b Bucket) time.Time {
	switch b {
	case BucketWeek:
		return d.AddDate(0, 0, 7)
	case BucketMonth:
		return d.AddDate(0, 1, 0)
	default:
		return d.AddDate(0, 0, 1)
	}
}

func countBuckets(from, to time.Time, b Bucket) int {
	n := 0
	for d := from; !d.After(to) && n <= maxPoints; d = next(d, b) {
		n++
	}
	return n
}
