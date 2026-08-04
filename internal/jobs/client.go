package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/danielmichaels/calendar-digest/internal/config"
	"log/slog"
	"net/http"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riversqlite"
	"github.com/riverqueue/river/rivermigrate"
	_ "modernc.org/sqlite"
	"riverqueue.com/riverui"
)

// maxWorkers bounds how many jobs run concurrently in this process.
const maxWorkers = 10

type Client struct {
	River *river.Client[*sql.Tx]
	ui    *riverui.Handler
	// worksJobs is false while workerRegistry is empty, in which case River is
	// built for inserts only and never started.
	worksJobs bool
}

// NewClient builds the job client and applies River's own migrations. Those
// are separate from the application schema: River owns its queue tables.
//
// db is the handle the rest of the process uses and is not closed here. A
// second handle would not break InsertTx — that runs through whatever *sql.Tx
// it is given — but it would put River's maintenance writes in contention with
// the application's over the same file, each waiting out the other's write
// lock for a full busy_timeout.
func NewClient(
	ctx context.Context,
	db *sql.DB,
	cfg *config.Conf,
	log *slog.Logger,
	deps *Deps,
) (*Client, error) {
	driver := riversqlite.New(db)
	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		return nil, err
	}
	// No advisory lock: SQLite is single-writer and single-node by nature.
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return nil, err
	}
	if deps == nil {
		deps = &Deps{}
	}
	deps.DB = db
	if deps.Log == nil {
		deps.Log = log
	}
	workers := NewWorkers(deps)

	// River refuses to start a client with an empty bundle, and equally refuses
	// one with no queues. Configuring the queue only when a worker exists makes
	// the difference between the two an insert-only client and a boot failure.
	riverCfg := &river.Config{Workers: workers, Logger: log}
	worksJobs := len(workerRegistry) > 0
	if worksJobs {
		riverCfg.Queues = map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: maxWorkers},
		}
		// The due check runs inside the worker, never here: a
		// PeriodicJobConstructor must never block, and this one would be
		// querying the database on every tick.
		//
		// RunOnStart because the schedule is in-memory and leader-scoped. A
		// restart resets it, so without this a deploy at 20:58 would wait a
		// full interval before first asking what is owed.
		riverCfg.PeriodicJobs = []*river.PeriodicJob{
			river.NewPeriodicJob(
				river.PeriodicInterval(cfg.AppConf.TickInterval),
				func() (river.JobArgs, *river.InsertOpts) {
					return ScanDueRecipientsArgs{}, nil
				},
				&river.PeriodicJobOpts{RunOnStart: true},
			),
			// RunOnStart is the entire point of this one: it turns "the
			// credential is dead" from something discovered at the notify time
			// into something discovered at deploy time.
			river.NewPeriodicJob(
				river.PeriodicInterval(24*time.Hour),
				func() (river.JobArgs, *river.InsertOpts) {
					return VerifyCalendarAccessArgs{}, nil
				},
				&river.PeriodicJobOpts{RunOnStart: true},
			),
		}
	}

	rc, err := river.NewClient(driver, riverCfg)
	if err != nil {
		return nil, err
	}
	// The workers were built before this existed, because the bundle is an
	// input to NewClient. They hold the pointer, so filling it here reaches
	// every one of them.
	deps.Jobs = rc

	c := &Client{River: rc, worksJobs: worksJobs}
	// Built only when it is going to be served: the handler keeps polling
	// caches of its own, so an unmounted one is a background cost for nothing.
	if cfg.AppConf.RiverUIEnabled {
		ui, err := riverui.NewHandler(&riverui.HandlerOpts{
			Endpoints: riverui.NewEndpoints(rc, nil),
			Logger:    log.With("component", "riverui"),
			Prefix:    cfg.AppConf.RiverUIPath,
		})
		if err != nil {
			return nil, fmt.Errorf("jobs: build river ui: %w", err)
		}
		c.ui = ui
	}
	return c, nil
}

// UIHandler is the job dashboard, for mounting at config RiverUIPath, or nil
// when RIVER_UI_EMBEDDED is off. Gate it behind whatever authorisation the
// rest of the admin area uses: it can cancel and retry jobs.
func (c *Client) UIHandler() http.Handler { return c.ui }

func (c *Client) Start(ctx context.Context) error {
	// The dashboard keeps background caches, so it needs starting too. It is
	// nil unless RIVER_UI_EMBEDDED asked for it.
	if c.ui != nil {
		if err := c.ui.Start(ctx); err != nil {
			return err
		}
	}
	if !c.worksJobs {
		return nil
	}
	return c.River.Start(ctx)
}

func (c *Client) Stop(ctx context.Context) error {
	if !c.worksJobs {
		return nil
	}
	return c.River.Stop(ctx)
}
