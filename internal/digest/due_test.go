package digest

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/store"
)

func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load location %s: %v", name, err)
	}
	return loc
}

func recipient(id int64, tz, notifyTime string) store.Recipients {
	return store.Recipients{
		ID:         id,
		Name:       fmt.Sprintf("recipient %d", id),
		CalendarID: fmt.Sprintf("cal-%d", id),
		NotifyTime: notifyTime,
		Tz:         tz,
		Enabled:    true,
	}
}

// done names a digest that already has a snapshot.
type done struct {
	recipientID int64
	digestDate  string
}

func snapshotKeys(keys ...done) []store.ListSnapshotKeysSinceRow {
	rows := make([]store.ListSnapshotKeysSinceRow, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, store.ListSnapshotKeysSinceRow{
			RecipientID: k.recipientID,
			DigestDate:  k.digestDate,
		})
	}
	return rows
}

func assertDue(t *testing.T, got, want []DueDigest) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("due = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("due[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestDue(t *testing.T) {
	bne := zone(t, "Australia/Brisbane")
	akl := zone(t, "Pacific/Auckland")
	hav := zone(t, "America/Havana")

	// Pacific/Auckland puts the clocks back on 2026-04-05 at 03:00, so 02:30
	// happens twice. These name the two instants a notify_time of "02:30" could
	// mean that day.
	firstOhTwoThirty := time.Date(2026, 4, 5, 1, 30, 0, 0, akl).Add(time.Hour)
	secondOhTwoThirty := firstOhTwoThirty.Add(time.Hour)

	tests := []struct {
		name       string
		now        time.Time
		recipients []store.Recipients
		snapshots  []store.ListSnapshotKeysSinceRow
		want       []DueDigest
	}{
		{
			name:       "nothing is owed before the notify time",
			now:        time.Date(2026, 8, 4, 20, 59, 0, 0, bne),
			recipients: []store.Recipients{recipient(1, "Australia/Brisbane", "21:00")},
			// Written last night at 21:00, which is why today has a digest and
			// tomorrow does not yet.
			snapshots: snapshotKeys(done{1, "2026-08-04"}),
			want:      nil,
		},
		{
			name:       "the notify time itself is due, not the tick after it",
			now:        time.Date(2026, 8, 4, 21, 0, 0, 0, bne),
			recipients: []store.Recipients{recipient(1, "Australia/Brisbane", "21:00")},
			snapshots:  snapshotKeys(done{1, "2026-08-04"}),
			want:       []DueDigest{{RecipientID: 1, DigestDate: "2026-08-05"}},
		},
		{
			name:       "a digest that already has a snapshot is not owed again",
			now:        time.Date(2026, 8, 4, 21, 5, 0, 0, bne),
			recipients: []store.Recipients{recipient(1, "Australia/Brisbane", "21:00")},
			snapshots:  snapshotKeys(done{1, "2026-08-04"}, done{1, "2026-08-05"}),
			want:       nil,
		},
		{
			// The whole point of a polled due check: 21:00 came and went with
			// the process down, and the morning tick still owes that date.
			name:       "a run missed overnight still owes the date it was for",
			now:        time.Date(2026, 8, 5, 6, 0, 0, 0, bne),
			recipients: []store.Recipients{recipient(1, "Australia/Brisbane", "21:00")},
			snapshots:  snapshotKeys(done{1, "2026-08-04"}),
			want:       []DueDigest{{RecipientID: 1, DigestDate: "2026-08-05"}},
		},
		{
			// 2026-08-05's digest was never made and the 5th is now over. A
			// digest of a day that has already happened is worse than none.
			name:       "a date that has passed is dropped, not delivered late",
			now:        time.Date(2026, 8, 6, 6, 0, 0, 0, bne),
			recipients: []store.Recipients{recipient(1, "Australia/Brisbane", "21:00")},
			snapshots:  snapshotKeys(done{1, "2026-08-04"}),
			want:       []DueDigest{{RecipientID: 1, DigestDate: "2026-08-06"}},
		},
		{
			// Down since 2026-08-01. Five missed notify instants, two dates
			// still ahead, and they come back in the order the days happen.
			name:       "a multi-day outage owes only the dates still ahead, oldest first",
			now:        time.Date(2026, 8, 6, 22, 0, 0, 0, bne),
			recipients: []store.Recipients{recipient(1, "Australia/Brisbane", "21:00")},
			snapshots:  nil,
			want: []DueDigest{
				{RecipientID: 1, DigestDate: "2026-08-06"},
				{RecipientID: 1, DigestDate: "2026-08-07"},
			},
		},
		{
			// One instant, two recipients, two different local dates: Auckland
			// is already on the 5th while Los Angeles is still on the 4th.
			// "Today" is per recipient or this is wrong for one of them.
			name: "recipients in different zones are on different local dates",
			now:  time.Date(2026, 8, 4, 14, 0, 0, 0, time.UTC),
			recipients: []store.Recipients{
				recipient(1, "Pacific/Auckland", "21:00"),
				recipient(2, "America/Los_Angeles", "21:00"),
			},
			snapshots: nil,
			want: []DueDigest{
				{RecipientID: 1, DigestDate: "2026-08-05"},
				{RecipientID: 2, DigestDate: "2026-08-04"},
			},
		},
		{
			// Clocks go forward 02:00 -> 03:00, so 02:30 has no instant of its
			// own and resolves to 03:30. At 03:29 that has not arrived.
			name:       "a notify time inside the spring-forward gap is not due before the gap closes",
			now:        time.Date(2026, 9, 27, 3, 29, 0, 0, akl),
			recipients: []store.Recipients{recipient(1, "Pacific/Auckland", "02:30")},
			snapshots:  snapshotKeys(done{1, "2026-09-27"}),
			want:       nil,
		},
		{
			name:       "a notify time inside the spring-forward gap is due once it resolves",
			now:        time.Date(2026, 9, 27, 3, 30, 0, 0, akl),
			recipients: []store.Recipients{recipient(1, "Pacific/Auckland", "02:30")},
			snapshots:  snapshotKeys(done{1, "2026-09-27"}),
			want:       []DueDigest{{RecipientID: 1, DigestDate: "2026-09-28"}},
		},
		{
			// time.Date resolves a repeated wall time to the second pass, so
			// the digest goes out an hour later than nominal on this one day.
			name:       "a notify time repeated by the fall-back is not due on the first pass",
			now:        firstOhTwoThirty,
			recipients: []store.Recipients{recipient(1, "Pacific/Auckland", "02:30")},
			snapshots:  snapshotKeys(done{1, "2026-04-05"}),
			want:       nil,
		},
		{
			name:       "a notify time repeated by the fall-back is due on the second pass",
			now:        secondOhTwoThirty,
			recipients: []store.Recipients{recipient(1, "Pacific/Auckland", "02:30")},
			snapshots:  snapshotKeys(done{1, "2026-04-05"}),
			want:       []DueDigest{{RecipientID: 1, DigestDate: "2026-04-06"}},
		},
		{
			// Havana's clocks jump at midnight on 2026-03-08, so time.Date
			// resolves 00:00 that day to 23:00 on the 7th. A digest date taken
			// from that instant is the 8th for the second time — a duplicate
			// against UNIQUE(recipient_id, digest_date), and the 9th never
			// sent. Taken from the calendar day it is the 9th.
			name:       "a midnight notify time in a midnight-transition zone keeps its date",
			now:        time.Date(2026, 3, 8, 5, 0, 0, 0, hav),
			recipients: []store.Recipients{recipient(1, "America/Havana", "00:00")},
			snapshots:  nil,
			want: []DueDigest{
				{RecipientID: 1, DigestDate: "2026-03-08"},
				{RecipientID: 1, DigestDate: "2026-03-09"},
			},
		},
		{
			name: "a disabled recipient is never owed a digest",
			now:  time.Date(2026, 8, 4, 21, 5, 0, 0, bne),
			recipients: []store.Recipients{
				func() store.Recipients {
					r := recipient(1, "Australia/Brisbane", "21:00")
					r.Enabled = false
					return r
				}(),
			},
			snapshots: nil,
			want:      nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, skipped := Due(tc.now, tc.recipients, tc.snapshots)
			if len(skipped) != 0 {
				t.Fatalf("skipped = %v, want none", skipped)
			}
			assertDue(t, got, tc.want)
		})
	}
}

// A recipient nobody can evaluate must not take the tick down with it, and must
// not disappear quietly either.
func TestDueReportsRecipientsItCannotEvaluate(t *testing.T) {
	bne := zone(t, "Australia/Brisbane")
	now := time.Date(2026, 8, 4, 21, 5, 0, 0, bne)

	recipients := []store.Recipients{
		recipient(1, "Australia/Brisbain", "21:00"),
		recipient(2, "Australia/Brisbane", "9pm"),
		recipient(3, "Australia/Brisbane", "21:00"),
	}

	got, skipped := Due(now, recipients, snapshotKeys(done{3, "2026-08-04"}))

	assertDue(t, got, []DueDigest{{RecipientID: 3, DigestDate: "2026-08-05"}})

	if len(skipped) != 2 {
		t.Fatalf("skipped = %v, want the two unusable recipients", skipped)
	}
	if skipped[0].RecipientID != 1 || skipped[0].Err == nil {
		t.Errorf("skipped[0] = %+v, want recipient 1 with a reason", skipped[0])
	}
	if skipped[1].RecipientID != 2 {
		t.Fatalf("skipped[1] = %+v, want recipient 2", skipped[1])
	}
	if !errors.Is(skipped[1].Err, ErrNotifyTimeFormat) {
		t.Errorf("skipped[1].Err = %v, want ErrNotifyTimeFormat", skipped[1].Err)
	}
}

func TestDueNotifyTimeFormats(t *testing.T) {
	bne := zone(t, "Australia/Brisbane")
	now := time.Date(2026, 8, 4, 23, 59, 0, 0, bne)

	tests := []struct {
		notifyTime string
		usable     bool
	}{
		{"21:00", true},
		{"00:00", true},
		{"09:05", true},
		{"9:00", true},
		{"21:00:00", false},
		{"21:0", false},
		{"24:00", false},
		{"23:60", false},
		{" 21:00", false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%q", tc.notifyTime), func(t *testing.T) {
			t.Parallel()
			owed, skipped := Due(
				now,
				[]store.Recipients{recipient(1, "Australia/Brisbane", tc.notifyTime)},
				nil,
			)
			if tc.usable {
				if len(skipped) != 0 {
					t.Fatalf("skipped = %v, want none", skipped)
				}
				if len(owed) == 0 {
					t.Error("owed nothing, want the dates every notify time before midnight owes")
				}
				return
			}
			if len(owed) != 0 {
				t.Errorf("owed = %v, want none", owed)
			}
			if len(skipped) != 1 || !errors.Is(skipped[0].Err, ErrNotifyTimeFormat) {
				t.Errorf("skipped = %v, want one ErrNotifyTimeFormat", skipped)
			}
		})
	}
}

// The floor is one value for a query that serves every recipient, so it has to
// be the earliest local date among them or the earliest recipient's already-done
// digest reads as still owed and sends twice.
func TestSnapshotFloor(t *testing.T) {
	// 2026-08-05 in Auckland, 2026-08-04 in Los Angeles.
	now := time.Date(2026, 8, 4, 14, 0, 0, 0, time.UTC)

	auckland := recipient(1, "Pacific/Auckland", "21:00")
	losAngeles := recipient(2, "America/Los_Angeles", "21:00")
	disabledLA := recipient(3, "America/Los_Angeles", "21:00")
	disabledLA.Enabled = false
	brokenLA := recipient(4, "America/Los_Angelez", "21:00")

	tests := []struct {
		name       string
		recipients []store.Recipients
		want       string
	}{
		{
			name:       "the earliest local date wins",
			recipients: []store.Recipients{auckland, losAngeles},
			want:       "2026-08-04",
		},
		{
			name:       "input order does not matter",
			recipients: []store.Recipients{losAngeles, auckland},
			want:       "2026-08-04",
		},
		{
			name:       "a disabled recipient does not drag the floor back",
			recipients: []store.Recipients{auckland, disabledLA},
			want:       "2026-08-05",
		},
		{
			name:       "an unloadable zone does not drag the floor back",
			recipients: []store.Recipients{auckland, brokenLA},
			want:       "2026-08-05",
		},
		{
			name:       "no recipients falls back to now in UTC",
			recipients: nil,
			want:       "2026-08-04",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := SnapshotFloor(now, tc.recipients); got != tc.want {
				t.Errorf("SnapshotFloor = %q, want %q", got, tc.want)
			}
		})
	}
}

// SnapshotFloor and Due have to agree: every date Due can owe must be inside
// the range the floor asks the database for, or a digest already sent reads as
// still owed.
func TestSnapshotFloorCoversEveryDateDueCanOwe(t *testing.T) {
	now := time.Date(2026, 8, 4, 14, 0, 0, 0, time.UTC)
	recipients := []store.Recipients{
		recipient(1, "Pacific/Auckland", "21:00"),
		recipient(2, "America/Los_Angeles", "21:00"),
		recipient(3, "Australia/Brisbane", "06:30"),
	}

	floor := SnapshotFloor(now, recipients)
	owed, skipped := Due(now, recipients, nil)
	if len(skipped) != 0 {
		t.Fatalf("skipped = %v, want none", skipped)
	}
	if len(owed) == 0 {
		t.Fatal("owed nothing, so this proves nothing")
	}
	for _, d := range owed {
		if d.DigestDate < floor {
			t.Errorf("owed %v, which is before the floor %q the query would use", d, floor)
		}
	}
}
