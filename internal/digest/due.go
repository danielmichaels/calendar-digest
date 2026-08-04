// Package digest decides which digests are owed. It is the application's
// clock, and it is deliberately pure: now is injected, nothing here reads the
// wall clock, the database or Google, so every timezone, DST and recovery case
// is a table test rather than a wait.
package digest

import (
	"errors"
	"fmt"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/store"
)

// ErrNotifyTimeFormat means a recipients.notify_time is not "HH:MM" on a
// 24-hour clock.
var ErrNotifyTimeFormat = errors.New("digest: notify_time is not HH:MM")

// DueDigest is one digest that should exist by now and does not.
type DueDigest struct {
	RecipientID int64
	// DigestDate is the local date the events fall on, "YYYY-MM-DD" in the
	// recipient's own zone — the day after the notify instant that owes it.
	DigestDate string
}

// Skipped is a recipient Due could not evaluate.
//
// Returned rather than swallowed: a recipient whose zone or notify_time no
// longer parses stops receiving digests forever, and that is indistinguishable
// from an empty calendar unless somebody says so.
type Skipped struct {
	RecipientID int64
	Err         error
}

// Due returns the digests owed at now that have no snapshot yet, each
// recipient's oldest date first, along with the recipients it could not
// evaluate.
//
// snapshots must be what ListSnapshotKeysSince returned for
// SnapshotFloor(now, recipients). A shorter range is not an error but is read
// as "not done yet", so a floor that is too late duplicates work rather than
// losing it.
//
// A digest stays owed only while its date has not itself passed in the
// recipient's zone: a run missed overnight still delivers the date it was for,
// and one missed for longer is dropped rather than delivered stale.
func Due(
	now time.Time,
	recipients []store.Recipients,
	snapshots []store.ListSnapshotKeysSinceRow,
) ([]DueDigest, []Skipped) {
	done := make(map[snapshotKey]struct{}, len(snapshots))
	for _, s := range snapshots {
		done[snapshotKey{recipientID: s.RecipientID, digestDate: s.DigestDate}] = struct{}{}
	}

	var owed []DueDigest
	var skipped []Skipped
	for _, r := range recipients {
		if !r.Enabled {
			continue
		}
		loc, err := time.LoadLocation(r.Tz)
		if err != nil {
			skipped = append(skipped, Skipped{
				RecipientID: r.ID,
				Err:         fmt.Errorf("digest: recipient %d: tz: %w", r.ID, err),
			})
			continue
		}
		hh, mm, err := parseNotifyTime(r.NotifyTime)
		if err != nil {
			skipped = append(skipped, Skipped{
				RecipientID: r.ID,
				Err:         fmt.Errorf("digest: recipient %d: %w", r.ID, err),
			})
			continue
		}

		for _, date := range candidates(now.In(loc), hh, mm, loc) {
			if _, ok := done[snapshotKey{recipientID: r.ID, digestDate: date}]; ok {
				continue
			}
			owed = append(owed, DueDigest{RecipientID: r.ID, DigestDate: date})
		}
	}
	return owed, skipped
}

// candidates returns the digest dates this recipient owes at now, oldest
// first, ignoring which of them already have a snapshot.
//
// now is already in loc, and hh:mm is the recipient's notify_time in that zone.
// The digest for local date D is owed once the notify instant on D-1 has
// passed, and stays owed only while D has not itself gone by — so however long
// the app was down, at most two dates are ever still in play.
//
// Build each date from the calendar day, never from the notify instant that
// day: at normalises a notify_time that lands inside a DST gap, and a gap at
// midnight normalises it onto the previous date.
func candidates(now time.Time, hh, mm int, loc *time.Location) []string {
	today := dateOf(now)
	var dates []string
	// One day back is the whole recovery window: the digest any earlier notify
	// instant owes is for a date that has already passed. Oldest first, so a
	// recovered digest arrives ahead of the current one.
	for _, back := range []int{1, 0} {
		day := today.AddDate(0, 0, -back)
		if now.Before(at(day, hh, mm, loc)) {
			continue
		}
		dates = append(dates, day.AddDate(0, 0, 1).Format(time.DateOnly))
	}
	return dates
}

// SnapshotFloor returns the earliest date Due could still owe at now: the
// earliest local "today" across the recipients, formatted for digest_date.
//
// One query serves every recipient, but "today" is per-zone — Auckland is
// already on tomorrow's date while Los Angeles is still on yesterday's — so the
// floor has to reach back to the earliest of them and let Due discard the rest
// per recipient. With no usable recipient it falls back to now in UTC, which
// costs nothing because Due returns nothing for that input either.
func SnapshotFloor(now time.Time, recipients []store.Recipients) string {
	floor := ""
	for _, r := range recipients {
		if !r.Enabled {
			continue
		}
		loc, err := time.LoadLocation(r.Tz)
		if err != nil {
			continue
		}
		// "YYYY-MM-DD" sorts lexicographically, which is also why the column
		// can be compared as text in SQL.
		if date := now.In(loc).Format(time.DateOnly); floor == "" || date < floor {
			floor = date
		}
	}
	if floor == "" {
		return now.UTC().Format(time.DateOnly)
	}
	return floor
}

type snapshotKey struct {
	recipientID int64
	digestDate  string
}

// dateOf reduces t to the calendar date it falls on in its own zone, carried as
// UTC midnight.
//
// Zoneless on purpose: AddDate on a zoned value can step into a DST gap and
// normalise onto a different calendar day — midnight in America/Havana on
// 2026-03-08 resolves to 23:00 on the 7th — which would silently mis-date a
// digest.
func dateOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// at returns the instant hh:mm on date falls on in loc, taking only the
// calendar day from date.
//
// A notify_time inside a DST gap has no instant of its own. time.Date
// normalises instead of failing, so 02:30 on a spring-forward day means 03:30
// that day: late once a year, never missed.
func at(date time.Time, hh, mm int, loc *time.Location) time.Time {
	y, m, d := date.Date()
	return time.Date(y, m, d, hh, mm, 0, 0, loc)
}

// parseNotifyTime reads a recipients.notify_time: "HH:MM" on a 24-hour clock,
// local to the recipient's tz.
func parseNotifyTime(s string) (hh, mm int, err error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %q", ErrNotifyTimeFormat, s)
	}
	return t.Hour(), t.Minute(), nil
}
