package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/danielmichaels/calendar-digest/internal/config"
	"log/slog"
	"net/http"

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
// db is the handle the rest of the process uses and is not closed here — that
// sharing is the point. River's tables living on the same connection is what
// lets InsertTx enqueue a job inside the transaction that writes the row the
// job is about, so the two commit or fail together.
func NewClient(
	ctx context.Context,
	db *sql.DB,
	cfg *config.Conf,
	log *slog.Logger,
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
	workers := river.NewWorkers()
	for _, register := range workerRegistry {
		register(workers)
	}

	// River refuses to start a client with an empty bundle, and equally refuses
	// one with no queues. Configuring the queue only when a worker exists makes
	// the difference between the two an insert-only client and a boot failure.
	riverCfg := &river.Config{Workers: workers, Logger: log}
	worksJobs := len(workerRegistry) > 0
	if worksJobs {
		riverCfg.Queues = map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: maxWorkers},
		}
	}

	rc, err := river.NewClient(driver, riverCfg)
	if err != nil {
		return nil, err
	}

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
