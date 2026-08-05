package config_test

import (
	"strings"
	"testing"

	"github.com/danielmichaels/calendar-digest/internal/config"
)

// setMinimalEnv supplies just enough for Load to succeed, so each test can
// break exactly one thing and see only that reported.
func setMinimalEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TRUSTED_ORIGINS", "https://trusted.example")
}

func TestLoadDefaults(t *testing.T) {
	setMinimalEnv(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.IsProduction() {
		t.Error("IsProduction() = true, want false: APP_ENV defaults to development")
	}
	if got := cfg.AppConf.SnapshotRetentionDays; got != 90 {
		t.Errorf("SnapshotRetentionDays = %d, want default 90", got)
	}
}

func TestSnapshotRetentionDaysMustBePositive(t *testing.T) {
	for _, value := range []string{"0", "-1"} {
		t.Run(value, func(t *testing.T) {
			setMinimalEnv(t)
			t.Setenv("SNAPSHOT_RETENTION_DAYS", value)

			if _, err := config.Load(); err == nil {
				t.Errorf("SNAPSHOT_RETENTION_DAYS=%q accepted, want rejected", value)
			}
		})
	}
}

// One pass should be enough to fix a misconfigured deployment, rather than one
// restart per missing variable.
func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("SERVER_PORT", "70000")
	// X_API_KEY is left at its default, which production rejects.

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() = nil error, want the production problems reported")
	}
	for _, want := range []string{"SERVER_PORT", "X_API_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s:\n%v", want, err)
		}
	}
}

// chi panics on overlapping mounts, so a bad RIVER_UI_PATH would kill the
// process at boot. Load rejects it with an explanation instead.
func TestRiverUIPathRejected(t *testing.T) {
	for _, path := range []string{"/", "/app", "/app/jobs", "/static", "/riverui/", "riverui"} {
		t.Run(path, func(t *testing.T) {
			setMinimalEnv(t)
			t.Setenv("RIVER_UI_EMBEDDED", "true")
			t.Setenv("RIVER_UI_PATH", path)

			if _, err := config.Load(); err == nil {
				t.Errorf("RIVER_UI_PATH=%q accepted, want rejected", path)
			}
		})
	}
}

// Nothing mounts when the dashboard is off, so the path cannot collide.
func TestRiverUIPathUncheckedWhenDisabled(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("RIVER_UI_EMBEDDED", "false")
	t.Setenv("RIVER_UI_PATH", "/app")

	if _, err := config.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
}

// Unset is a warning at boot, not a startup failure: a digest with no link is
// still a digest, where a process that refuses to start delivers nothing.
func TestBaseURLMayBeUnset(t *testing.T) {
	setMinimalEnv(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.AppConf.BaseURL != "" {
		t.Errorf("BaseURL = %q, want empty when BASE_URL is unset", cfg.AppConf.BaseURL)
	}
}

// Unset and wrong are different answers. Nothing can be built from a value
// that is not a URL, and every notification would carry the result.
func TestBaseURLRejectsWhatIsNotAnAbsoluteURL(t *testing.T) {
	for _, value := range []string{
		"calendar.int.lookout.wiki", // the form it is written in everywhere, and not a URL
		"/d",
		"://nope",
		"ftp://calendar.int.lookout.wiki",
		"https://",
	} {
		t.Run(value, func(t *testing.T) {
			setMinimalEnv(t)
			t.Setenv("BASE_URL", value)

			if _, err := config.Load(); err == nil {
				t.Errorf("BASE_URL=%q accepted, want rejected", value)
			}
		})
	}
}

// Renderers concatenate rather than resolve, so the trailing slash is removed
// once here instead of being guarded at every call site.
func TestBaseURLLosesItsTrailingSlash(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("BASE_URL", "https://calendar.int.lookout.wiki/")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.AppConf.BaseURL; got != "https://calendar.int.lookout.wiki" {
		t.Errorf("BaseURL = %q, want the trailing slash gone", got)
	}
}

func TestRiverUIDefaultsAreUsable(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("RIVER_UI_EMBEDDED", "true")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.AppConf.RiverUIPath; got != "/riverui" {
		t.Errorf("RiverUIPath = %q, want /riverui", got)
	}
}
