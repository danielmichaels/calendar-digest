package cmd

import (
	"fmt"

	"github.com/danielmichaels/calendar-digest/internal/calendar"
	"github.com/danielmichaels/calendar-digest/internal/deliver"
	"github.com/danielmichaels/calendar-digest/internal/jobs"
	"github.com/danielmichaels/calendar-digest/internal/notify"
	"github.com/danielmichaels/calendar-digest/internal/server"
)

type ServeCmd struct {
}

// buildNotifiers wires one delivery implementation per configured channel.
//
// A channel whose configuration is missing is left out rather than stubbed, so
// a target of that kind fails its send with jobs.ErrNoNotifier and says so —
// where a notifier that quietly did nothing would set notified_at.
//
// SMS is the exception and is always registered: it refuses on purpose, and
// logs the payload the webhook will eventually have to accept.
func buildNotifiers(app *App) map[string]jobs.Notifier {
	cfg := app.Config
	base := cfg.AppConf.BaseURL
	if base == "" {
		app.Logger.Warn("BASE_URL is unset: digests will go out with no link to their detail page")
	}

	notifiers := []jobs.Notifier{
		&deliver.SMSNotifier{Renderer: deliver.SMSRenderer{BaseURL: base}, Log: app.Logger},
	}

	if cfg.AppConf.TelegramBotToken == "" {
		app.Logger.Warn("TELEGRAM_BOT_TOKEN is unset: telegram targets cannot be delivered")
	} else {
		notifiers = append(notifiers, &deliver.TelegramNotifier{
			Bot:      &notify.Telegram{Token: cfg.AppConf.TelegramBotToken},
			Renderer: deliver.TelegramRenderer{BaseURL: base},
		})
	}

	switch {
	case cfg.Email.Host == "":
		app.Logger.Warn("SMTP_HOST is unset: email targets cannot be delivered")
	case cfg.Email.From == "":
		app.Logger.Warn("EMAIL_FROM is unset: email targets cannot be delivered")
	default:
		notifiers = append(notifiers, &deliver.EmailNotifier{
			Sender: &deliver.SMTPSender{
				Host:     cfg.Email.Host,
				Port:     cfg.Email.Port,
				Username: cfg.Email.Username,
				Password: cfg.Email.Password,
				From:     cfg.Email.From,
			},
			Renderer: deliver.EmailRenderer{BaseURL: base},
		})
	}

	return jobs.RegisterNotifiers(notifiers...)
}

func (s *ServeCmd) Run() error {
	app, err := NewApp()
	if err != nil {
		return err
	}
	defer app.Close()

	notifiers := buildNotifiers(app)
	deps := server.Deps{
		Conf: app.Config,
		Log:  app.Logger,
		Db:   app.Store,
		// The same map the workers use, so a "send test" from the UI proves the
		// configuration a nightly run depends on.
		Notifiers: notifiers,
	}

	jobDeps := &jobs.Deps{Notifiers: notifiers}
	if credential := app.Config.AppConf.GoogleServiceAccountJSON; credential != "" {
		cal, err := calendar.NewGoogleClient(app.Ctx, credential)
		if err != nil {
			return fmt.Errorf("build calendar client: %w", err)
		}
		jobDeps.Calendar = cal
	} else {
		app.Logger.Warn("GOOGLE_SERVICE_ACCOUNT_JSON is unset: no digest can be captured")
	}

	// Wired before the client starts, so the boot-time calendar check has
	// somewhere to shout. Missing configuration is warned about rather than
	// fatal: a running app that logs its alerts beats one that will not start.
	switch {
	case app.Config.AppConf.TelegramBotToken == "":
		app.Logger.Warn("TELEGRAM_BOT_TOKEN is unset: alerts will only reach the log")
	case app.Config.AppConf.AlertTelegramChatID == "":
		app.Logger.Warn("ALERT_TELEGRAM_CHAT_ID is unset: alerts will only reach the log")
	default:
		jobDeps.Alerter = &notify.TelegramAlerter{
			Bot:    &notify.Telegram{Token: app.Config.AppConf.TelegramBotToken},
			ChatID: app.Config.AppConf.AlertTelegramChatID,
		}
	}

	jobClient, err := jobs.NewClient(app.Ctx, app.DB, app.Config, app.Logger, jobDeps)
	if err != nil {
		return fmt.Errorf("create job client: %w", err)
	}
	deps.Jobs = jobClient
	srv := server.New(deps)

	if err := srv.Start(app.Ctx); err != nil {
		return fmt.Errorf("start background workers: %w", err)
	}
	defer func() {
		if err := srv.Stop(app.Ctx); err != nil {
			app.Logger.Error("stopping background workers", "error", err)
		}
	}()

	if err := srv.Serve(app.Ctx); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
