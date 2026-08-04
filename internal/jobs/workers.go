package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/danielmichaels/calendar-digest/internal/calendar"
	"github.com/danielmichaels/calendar-digest/internal/digest"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// Enqueuer is the slice of the River client the workers are allowed to use.
//
// Narrow deliberately: a worker holding the whole client could start it, stop
// it or cancel other jobs. InsertManyTx is here alongside InsertMany because
// the fan-out has to land in the same transaction as the snapshot it depends
// on, and InsertMany opens one of its own.
type Enqueuer interface {
	InsertMany(
		ctx context.Context,
		params []river.InsertManyParams,
	) ([]*rivertype.JobInsertResult, error)
	InsertManyTx(
		ctx context.Context,
		tx *sql.Tx,
		params []river.InsertManyParams,
	) ([]*rivertype.JobInsertResult, error)
}

// Notifier delivers one digest over one channel.
//
// The interface lives here, where it is consumed, so the implementations can
// arrive later without the job layer changing. Send returns the body it sent
// so a caller can log what a recipient actually received, and must return a
// non-nil error whenever delivery did not happen — a nil error is what sets
// notified_at.
type Notifier interface {
	// Kind matches notification_targets.kind.
	Kind() string
	Send(ctx context.Context, target json.RawMessage, d digest.Digest) (body string, err error)
}

// Deps is everything the workers need.
//
// Taken by pointer throughout: Jobs cannot be filled until river.NewClient has
// returned, and that call needs the worker bundle these dependencies are
// injected into. The workers hold the box, and NewClient fills it.
type Deps struct {
	DB       *sql.DB
	Calendar calendar.Client
	// Notifiers is keyed by notification_targets.kind. A target whose kind has
	// no entry fails its send rather than being silently dropped.
	Notifiers map[string]Notifier
	// Alerter reaches the operator, not a recipient. Nil leaves alerts at ERROR
	// in the log, which is a worse place to find out but not a silent one.
	Alerter Alerter
	Log     *slog.Logger
	// Now is the clock the due check reads. Nil means time.Now.
	Now func() time.Time
	// Jobs enqueues follow-on work; NewClient sets it to the client it built.
	Jobs Enqueuer
}

func (d *Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Deps) log() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.Default()
}

// workerRegistry is every worker this process runs. Adding one here is all that
// is required to make it available, and a non-empty list is also what makes
// jobs.Client configure a queue and start rather than run insert-only.
//
// The list is registration functions rather than worker values because
// river.AddWorker is generic over the job args: a []river.Worker cannot be
// written down, but a slice of closures that each add one can.
var workerRegistry = []func(*river.Workers, *Deps){
	func(w *river.Workers, d *Deps) { river.AddWorker(w, &ScanDueRecipientsWorker{Deps: d}) },
	func(w *river.Workers, d *Deps) { river.AddWorker(w, &DigestWorker{Deps: d}) },
	func(w *river.Workers, d *Deps) { river.AddWorker(w, &SendWorker{Deps: d}) },
	func(w *river.Workers, d *Deps) { river.AddWorker(w, &AlertWorker{Deps: d}) },
	func(w *river.Workers, d *Deps) { river.AddWorker(w, &VerifyCalendarAccessWorker{Deps: d}) },
}

// NewWorkers builds the bundle NewClient registers.
//
// Exported because River rejects an insert whose kind is not in the bundle, so
// anything that only wants to enqueue still needs the full set.
func NewWorkers(deps *Deps) *river.Workers {
	workers := river.NewWorkers()
	for _, register := range workerRegistry {
		register(workers, deps)
	}
	return workers
}
