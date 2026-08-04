module github.com/danielmichaels/calendar-digest

go 1.26

// Direct dependencies only. `task init` runs `go mod tidy`, which resolves the
// indirect set and writes go.sum.
require (
	github.com/SladkyCitron/slogcolor v1.9.0
	github.com/a-h/templ v0.3.1020
	github.com/alecthomas/kong v1.16.0
	github.com/alexedwards/scs/v2 v2.9.0
	github.com/danielgtaylor/huma/v2 v2.39.1
	github.com/go-chi/chi/v5 v5.3.1
	github.com/go-chi/httplog/v3 v3.4.0
	github.com/joeshaw/envdecode v0.0.0-20200121155833-099f1fc765bd
	github.com/pressly/goose/v3 v3.27.3
	github.com/riverqueue/river v0.41.1
	github.com/riverqueue/river/riverdriver/riversqlite v0.41.1
	golang.org/x/sync v0.22.0
	modernc.org/sqlite v1.55.0
	riverqueue.com/riverui v0.16.0
)

require (
	github.com/a-h/parse v0.0.0-20250122154542-74294addb73e // indirect
	github.com/andybalholm/brotli v1.2.2 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cli/browser v1.3.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/fatih/color v1.16.0 // indirect
	github.com/fsnotify/fsnotify v1.7.0 // indirect
	github.com/fxamacker/cbor/v2 v2.9.2 // indirect
	github.com/gabriel-vasile/mimetype v1.4.13 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.30.3 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgerrcode v0.0.0-20250907135507-afb5586c32a6 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.10.0 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.23 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/natefinch/atomic v1.0.1 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/riverqueue/apiframe v0.0.0-20251229202423-2b52ce1c482e // indirect
	github.com/riverqueue/river/riverdriver v0.41.1 // indirect
	github.com/riverqueue/river/rivershared v0.41.1 // indirect
	github.com/riverqueue/river/rivertype v0.41.1 // indirect
	github.com/sethvargo/go-retry v0.4.0 // indirect
	github.com/stretchr/testify v1.11.1 // indirect
	github.com/tidwall/gjson v1.19.0 // indirect
	github.com/tidwall/match v1.2.0 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.uber.org/goleak v1.3.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/mod v0.38.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	golang.org/x/tools v0.47.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	modernc.org/libc v1.74.3 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

// templ generates the Go code behind every .templ file. Pinning the generator
// here keeps `go tool templ generate` on the same version as the runtime
// library the generated code compiles against.
tool github.com/a-h/templ/cmd/templ
