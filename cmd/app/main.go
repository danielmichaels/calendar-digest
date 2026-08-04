package main

import (
	"fmt"
	"os"

	"github.com/danielmichaels/calendar-digest/internal/cmd"
	"github.com/danielmichaels/calendar-digest/internal/version"

	"github.com/alecthomas/kong"

	_ "modernc.org/sqlite"
)

const appName = "calendar-digest"

type VersionFlag string

func (v VersionFlag) Decode(_ *kong.DecodeContext) error { return nil }
func (v VersionFlag) IsBool() bool                       { return true }
func (v VersionFlag) BeforeApply(app *kong.Kong, vars kong.Vars) error {
	fmt.Println(vars["version"])
	app.Exit(0)
	return nil
}

type CLI struct {
	cmd.Globals

	Version     VersionFlag        `       help:"Print version information and quit" short:"v" name:"version"`
	Serve       cmd.ServeCmd       `cmd:"" help:"Run a server instance"`
	Migrate     cmd.MigrateCmd     `cmd:"" help:"Apply pending database migrations and exit"`
	Healthcheck cmd.HealthcheckCmd `cmd:"" help:"Probe this instance over loopback; used by the container HEALTHCHECK"`
}

func run() error {
	ver := version.Get()
	if ver == "unavailable" {
		ver = "development"
	}
	cli := CLI{
		Version: VersionFlag(ver),
	}
	// Display help if no args are provided instead of an error message
	if len(os.Args) < 2 {
		os.Args = append(os.Args, "--help")
	}

	ctx := kong.Parse(&cli,
		kong.Name(appName),
		kong.Description("Nightly per-recipient digest of tomorrow's Google Calendar events"),
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{
			Compact: true,
		}),
		kong.DefaultEnvars(appName),
		kong.Vars{
			"version": string(cli.Version),
		})
	err := ctx.Run(&cli.Globals)
	ctx.FatalIfErrorf(err)
	return nil
}

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}
