package calendar

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Fake is a Client for tests: it answers from a fixed map and records what it
// was asked for.
//
// It is safe for concurrent use because the jobs that drive it are not
// serialised — several DigestJobs can be in flight at once.
type Fake struct {
	mu    sync.Mutex
	days  map[string][]Event
	calls []FakeCall
	// Err, when set, fails every call. Set it to exercise a retry path.
	Err error
}

// FakeCall is one recorded request.
type FakeCall struct {
	CalendarID string
	Date       string
	Zone       string
}

func NewFake() *Fake {
	return &Fake{days: make(map[string][]Event)}
}

// Set gives calendarID the supplied events on date. A day never Set answers
// with no events rather than an error — an empty day is a real answer, and the
// digest sends a confirmation for it.
func (f *Fake) Set(calendarID, date string, events ...Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.days[fakeKey(calendarID, date)] = events
}

func (f *Fake) EventsForDay(
	_ context.Context,
	calendarID, date string,
	loc *time.Location,
) ([]Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, FakeCall{CalendarID: calendarID, Date: date, Zone: loc.String()})
	if f.Err != nil {
		return nil, f.Err
	}
	// Never nil: the result is JSON-encoded into the snapshot, where a nil
	// slice would store "null" and an empty one "[]".
	out := make([]Event, 0, len(f.days[fakeKey(calendarID, date)]))
	return append(out, f.days[fakeKey(calendarID, date)]...), nil
}

func (f *Fake) VerifyAccess(_ context.Context, calendarID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, FakeCall{CalendarID: calendarID, Date: verifyCall})
	return f.Err
}

// verifyCall marks a recorded call as a VerifyAccess rather than a day fetch,
// since that one has no date of its own.
const verifyCall = "verify"

// Calls returns what the fake was asked for, in order.
func (f *Fake) Calls() []FakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]FakeCall(nil), f.calls...)
}

func fakeKey(calendarID, date string) string {
	return fmt.Sprintf("%s|%s", calendarID, date)
}
