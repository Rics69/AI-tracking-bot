package access

import "time"

// SetClock moves the clock in tests.
func SetClock(s *Service, now func() time.Time) { s.now = now }
