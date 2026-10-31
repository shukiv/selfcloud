// SPDX-License-Identifier: AGPL-3.0-or-later

// Command selfcloud is a self-hosted drive server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shukiv/selfcloud/internal/api"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/fsutil"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/thumb"
	"github.com/shukiv/selfcloud/internal/version"
)

const usage = `Usage: selfcloud <command> [-config FILE]

Commands:
  serve         run the server
  migrate       apply database migrations and exit
  admin         create admin keys and accounts (selfcloud admin help)
  healthcheck   exit 0 if the local server is ready (for container health checks)
  version       print the version

Configuration comes from defaults, then the YAML file named by -config or
$SELFCLOUD_CONFIG, then SELFCLOUD_* environment variables.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "serve":
		return serve(args[1:], stdout, stderr)
	case "migrate":
		return migrate(args[1:], stdout, stderr)
	case "admin":
		return admin(args[1:], stdout, stderr)
	case "healthcheck":
		return healthcheck(args[1:], stderr)
	case "thumb":
		// The decoder's child process (internal/thumb). It is started by the server, with limits, and reads no config.
		return thumb.Child(args[1:], os.Stdin, stdout, stderr)
	case "version", "-version", "--version":
		fmt.Fprintln(stdout, version.Version)
		return 0
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "selfcloud: unknown command %q\n\n%s", args[0], usage)
	return 2
}

// loadConfig parses the command's flags (-config, plus any that setup adds), checks that
// exactly nargs positional arguments follow, and loads the configuration. It reports errors
// itself; when cfg is nil the caller exits with code.
func loadConfig(name string, args []string, stderr io.Writer, nargs int, setup func(*flag.FlagSet)) (
	cfg *config.Config, rest []string, code int) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", os.Getenv("SELFCLOUD_CONFIG"), "configuration `file` (YAML)")
	if setup != nil {
		setup(fs)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, nil, 0
		}
		return nil, nil, 2
	}
	if fs.NArg() != nargs {
		fmt.Fprintf(stderr, "selfcloud %s: expected %d argument(s), got %d\n", name, nargs, fs.NArg())
		return nil, nil, 2
	}
	cfg, err := config.Load(*path, os.LookupEnv)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil, nil, 2
	}
	return cfg, fs.Args(), 0
}

func newLogger(c config.LogConfig, w io.Writer) *slog.Logger {
	var level slog.Level
	_ = level.UnmarshalText([]byte(c.Level)) // validated by config.Load
	opts := &slog.HandlerOptions{Level: level}
	if c.Format == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}

func openStore(ctx context.Context, cfg *config.Config, log *slog.Logger) (*store.Store, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	if info, err := fsutil.Type(cfg.DataDir); err == nil && info.Network {
		log.Warn("the data directory is on a network file system; uploads will be slower and "+
			"less safe than on a local disk", "dir", cfg.DataDir, "fs", info.Name)
	}
	return store.Open(ctx, store.Options{
		Path:           cfg.Store.Path,
		AllowNetworkFS: cfg.Store.AllowNetworkFS,
		Logger:         log,
	})
}

// newMailer returns the SMTP sender for the configuration, or nil when mail is not configured. The
// password is read from its file here, so a missing file stops the server at start.
func newMailer(c config.MailConfig) (mail.Sender, error) {
	if !c.Configured() {
		return nil, nil
	}
	pw, err := mail.ReadSecretFile(c.SMTPPasswordFile)
	if err != nil {
		return nil, fmt.Errorf("mail.smtp_password_file: %w", err)
	}
	smtp, err := mail.NewSMTP(mail.SMTPConfig{Host: c.SMTPHost, Port: c.SMTPPort, Username: c.SMTPUsername,
		Password: pw, TLS: c.SMTPTLS, From: c.From})
	if err != nil {
		return nil, err
	}
	return smtp, nil
}

func serve(args []string, stdout, stderr io.Writer) int {
	cfg, _, code := loadConfig("serve", args, stderr, 0, nil)
	if cfg == nil {
		return code
	}
	log := newLogger(cfg.Log, stdout)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("starting selfcloud", "version", version.Version)
	st, err := openStore(ctx, cfg, log)
	if err != nil {
		log.Error("cannot open the database", "error", err)
		return 1
	}
	defer st.Close()
	if cfg.AdminKeyFile != "" {
		if err := bootstrapAdminKey(ctx, st, cfg.AdminKeyFile, log); err != nil {
			log.Error("cannot load admin_key_file", "error", err)
			return 1
		}
	}

	mailer, err := newMailer(cfg.Mail)
	if err != nil {
		log.Error("cannot configure mail", "error", err)
		return 1
	}
	srv, err := api.New(api.Options{Config: cfg, Logger: log, Store: st, Mailer: mailer})
	if err != nil {
		log.Error("cannot start", "error", err)
		return 1
	}
	if err := srv.Run(ctx); err != nil {
		log.Error("server failed", "error", err)
		return 1
	}
	log.Info("stopped")
	return 0
}

func migrate(args []string, stdout, stderr io.Writer) int {
	cfg, _, code := loadConfig("migrate", args, stderr, 0, nil)
	if cfg == nil {
		return code
	}
	log := newLogger(cfg.Log, stderr)
	ctx := context.Background()
	st, err := openStore(ctx, cfg, log)
	if err != nil {
		fmt.Fprintln(stderr, "selfcloud migrate:", err)
		return 1
	}
	defer st.Close()
	v, err := st.SchemaVersion(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "selfcloud migrate:", err)
		return 1
	}
	fmt.Fprintf(stdout, "database schema is at version %d\n", v)
	return 0
}

// healthcheck asks the server on this host whether it is ready.
func healthcheck(args []string, stderr io.Writer) int {
	cfg, _, code := loadConfig("healthcheck", args, stderr, 0, nil)
	if cfg == nil {
		return code
	}
	host, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		fmt.Fprintln(stderr, "selfcloud healthcheck: listen:", err)
		return 1
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/readyz")
	if err != nil {
		fmt.Fprintln(stderr, "selfcloud healthcheck:", err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(stderr, "selfcloud healthcheck:", resp.Status)
		return 1
	}
	return 0
}
