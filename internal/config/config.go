package config

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/joeshaw/envdecode"
)

// EnvProduction is the APP_ENV value that turns the development-only defaults
// below into hard configuration errors.
const EnvProduction = "production"

type Conf struct {
	Server  serverConf
	Db      dbConf
	Limiter limiter
	AppConf appConf
	Session sessionConf
}

type limiter struct {
	Enabled bool          `env:"RATE_LIMIT_ENABLED,default=true"`
	Rps     int           `env:"RATE_LIMIT_RPS,default=10"`
	BackOff time.Duration `env:"RATE_LIMIT_BACKOFF,default=20s"`
}

type dbConf struct {
	DbName                    string        `env:"DATABASE_URL,default=database/data.db"`
	DatabaseConnectionContext time.Duration `env:"DATABASE_CONNECTION_CONTEXT,default=15s"`
}
type serverConf struct {
	XApiKey      string        `env:"X_API_KEY,default=changeme"`
	Port         int           `env:"SERVER_PORT,default=9898"`
	TimeoutRead  time.Duration `env:"SERVER_TIMEOUT_READ,default=5s"`
	TimeoutWrite time.Duration `env:"SERVER_TIMEOUT_WRITE,default=10s"`
	TimeoutIdle  time.Duration `env:"SERVER_TIMEOUT_IDLE,default=15s"`
}

// sessionConf configures server-side sessions. There is no secret to set:
// scs stores session data in Postgres and the cookie carries only an opaque
// token.
type sessionConf struct {
	Lifetime time.Duration `env:"SESSION_LIFETIME,default=168h"`
	// Secure must be true anywhere the site is served over HTTPS.
	Secure bool `env:"SESSION_COOKIE_SECURE,default=false"`
}
type appConf struct {
	Env                string     `env:"APP_ENV,default=development"`
	LogLevel           slog.Level `env:"LOG_LEVEL,default=info"`
	LogJson            bool       `env:"LOG_JSON,default=false"`
	LogConcise         bool       `env:"LOG_CONCISE,default=false"`
	LogResponseHeaders bool       `env:"LOG_RESPONSE_HEADERS,default=false"`
	LogRequestHeaders  bool       `env:"LOG_REQUEST_HEADERS,default=true"`
	// TrustedOrigins are the cross-origin sites allowed to submit to this app,
	// as scheme://host (e.g. https://app.example.com).
	TrustedOrigins []string `env:"TRUSTED_ORIGINS"`
	// RiverUIEnabled mounts the job dashboard inside this binary. It ships off
	// because the dashboard can cancel and retry jobs and nothing in a freshly
	// generated project authorises anything — see the mount site in
	// internal/server/routes.go for where the gate goes.
	RiverUIEnabled bool `env:"RIVER_UI_EMBEDDED,default=false"`
	// RiverUIPath is checked against the paths Routes already mounts.
	RiverUIPath string `env:"RIVER_UI_PATH,default=/riverui"`
}

// IsProduction reports whether this process is configured as a deployment
// rather than a developer's machine.
func (c *Conf) IsProduction() bool { return c.AppConf.Env == EnvProduction }

// mountedPrefixes are the paths Routes already owns. chi panics when two
// mounts overlap, so an unlucky RIVER_UI_PATH would take the whole process
// down at boot rather than 404 on one route.
var mountedPrefixes = []string{"/app", "/static", "/docs", "/healthz", "/version", "/openapi.json"}

// riverUIPathProblem returns the empty string when the path is usable.
func riverUIPathProblem(path string) string {
	switch {
	case !strings.HasPrefix(path, "/"):
		return "RIVER_UI_PATH must start with /"
	case path == "/":
		return "RIVER_UI_PATH must not be /: the dashboard would answer every route"
	case strings.HasSuffix(path, "/"):
		return "RIVER_UI_PATH must not end with /"
	}
	for _, prefix := range mountedPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return fmt.Sprintf("RIVER_UI_PATH must not sit under %s, which is already mounted", prefix)
		}
	}
	return ""
}

// Load reads configuration from the environment.
//
// Every problem is reported at once rather than one per restart: a
// misconfigured deployment should need a single pass to fix, not one
// deploy per missing variable.
func Load() (*Conf, error) {
	var c Conf
	if err := envdecode.StrictDecode(&c); err != nil {
		return nil, fmt.Errorf("config: decode: %w", err)
	}

	var problems []string

	if c.Server.Port < 1 || c.Server.Port > 65535 {
		problems = append(problems, "SERVER_PORT must be between 1 and 65535")
	}
	if c.AppConf.RiverUIEnabled {
		if problem := riverUIPathProblem(c.AppConf.RiverUIPath); problem != "" {
			problems = append(problems, problem)
		}
	}
	if c.IsProduction() {
		if c.Server.XApiKey == "changeme" {
			problems = append(problems, "X_API_KEY must be changed from its default in production")
		}
		if len(c.AppConf.TrustedOrigins) == 0 {
			problems = append(problems, "TRUSTED_ORIGINS must list at least one origin in production")
		}
		if !c.Session.Secure {
			problems = append(problems, "SESSION_COOKIE_SECURE must be true in production")
		}
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("config:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return &c, nil
}
