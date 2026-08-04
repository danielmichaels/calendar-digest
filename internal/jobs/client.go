package jobs

import (
	"context"
	"log/slog"
"fmt"
"net/http"
"github.com/danielmichaels/calendar-digest/internal/config"
"database/sql"

	"github.com/riverqueue/river/riverdriver/riversqlite"
	_ "modernc.org/sqlite"
"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivermigrate"
"riverqueue.com/riverui"
)

// maxWorkers bounds how many jobs run concurrently in this process.
const maxWorkers = 10

type Client struct {
River *river.Client[*sql.Tx]
	db    *sql.DB
ui *riverui.Handler
}

// NewClient builds the job client and applies River's own migrations. Those
// are separate from the application schema: River owns its queue tables.
func NewClient(
	ctx context.Context,
	dbPath string,
	cfg *config.Conf,
	log *slog.Logger,
) (*Client, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	driver := riversqlite.New(db)
migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
db.Close()
return nil, err
	}
// No advisory lock: SQLite is single-writer and single-node by nature.
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		db.Close()
		return nil, err
	}
workers := river.NewWorkers()
	river.AddWorker(workers, &ExampleWorker{})

	rc, err := river.NewClient(driver, &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: maxWorkers},
		},
		Workers: workers,
		Logger:  log,
	})
	if err != nil {
db.Close()
return nil, err
	}

c := &Client{River: rc, db: db}
// Built only when it is going to be served: the handler keeps polling
	// caches of its own, so an unmounted one is a background cost for nothing.
	if cfg.AppConf.RiverUIEnabled {
		ui, err := riverui.NewHandler(&riverui.HandlerOpts{
			Endpoints: riverui.NewEndpoints(rc, nil),
			Logger:    log.With("component", "riverui"),
			Prefix:    cfg.AppConf.RiverUIPath,
		})
		if err != nil {
db.Close()
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
return c.River.Start(ctx)
}

func (c *Client) Stop(ctx context.Context) error {
	return c.River.Stop(ctx)
}
