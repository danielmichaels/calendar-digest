package store

import (
	"context"
"time"

	"github.com/danielmichaels/calendar-digest/internal/config"

"database/sql"

	_ "modernc.org/sqlite"
)

func NewDatabasePool(ctx context.Context, cfg *config.Conf) (*sql.DB, error) {
	db, err := sql.Open("sqlite", cfg.Db.DbName)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err = db.PingContext(ctx); err != nil {
		return nil, err
	}
	return db, nil
}
