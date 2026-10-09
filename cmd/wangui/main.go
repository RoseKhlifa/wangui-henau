package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"wangui/internal/api"
	"wangui/internal/backup"
	"wangui/internal/config"
	"wangui/internal/events"
	"wangui/internal/notify"
	"wangui/internal/scheduler"
	"wangui/internal/store"
	"wangui/internal/web"
)

const usage = `wangui — Henau late-return auto check-in (single tenant, Phase 1)

Usage:
  wangui <command> [flags]

Commands:
  doctor       Validate config + token + rules            (single-tenant)
  status       Fetch current checkin status               (single-tenant)
  sign         Perform a single sign-in attempt right now (single-tenant)
  daemon       Run forever; sign automatically            (single-tenant)
  serve        Start the multi-tenant web server          (Phase 2)
  backup-now   Run one SQLite backup right now            (ops / test)
  help         Show this help

Flags for doctor/status/sign/daemon:
  -c <path>      Config file (default: ./config.yaml)

Flags for serve:
  -addr <host:port>  Bind address (default: 127.0.0.1:4444)
  -data <dir>        Data directory for sqlite + master key (default: ./data)

Environment:
  WANGUI_MASTER_KEY  Hex-encoded 32-byte AES key (encrypts user tokens).
                     If unset, a key is generated at <data>/master.key (dev only).
  WANGUI_ADMIN_PASS  Plain-text admin password. If unset, admin panel is disabled.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd := os.Args[1]

	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(os.Stdout, usage)
	case "doctor", "status", "sign", "daemon":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		configPath := fs.String("c", "config.yaml", "config file path")
		_ = fs.Parse(os.Args[2:])
		switch cmd {
		case "doctor":
			exit(runDoctor(*configPath))
		case "status":
			exit(runStatus(*configPath))
		case "sign":
			exit(runSign(*configPath))
		case "daemon":
			exit(runDaemon(*configPath))
		}
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		addr := fs.String("addr", "127.0.0.1:4444", "bind address")
		dataDir := fs.String("data", "data", "data directory")
		_ = fs.Parse(os.Args[2:])
		exit(runServe(*addr, *dataDir))
	case "backup-now":
		fs := flag.NewFlagSet("backup-now", flag.ExitOnError)
		dataDir := fs.String("data", "data", "data directory")
		_ = fs.Parse(os.Args[2:])
		exit(runBackupNow(*dataDir))
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n%s", cmd, usage)
		os.Exit(2)
	}
}

func runBackupNow(dataDir string) error {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	crypto, err := store.LoadCrypto(filepath.Join(dataDir, "master.key"))
	if err != nil {
		return fmt.Errorf("master key: %w", err)
	}
	st, err := store.Open(filepath.Join(dataDir, "wangui.db"), crypto)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()
	bkp := &backup.Backup{
		DB:  st.DB(),
		Dir: filepath.Join(dataDir, "backups"),
		Log: logger,
	}
	if err := os.MkdirAll(bkp.Dir, 0o700); err != nil {
		return err
	}
	return bkp.RunOnce(context.Background())
}

func runServe(addr, dataDir string) error {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	crypto, err := store.LoadCrypto(filepath.Join(dataDir, "master.key"))
	if err != nil {
		return fmt.Errorf("master key: %w", err)
	}
	st, err := store.Open(filepath.Join(dataDir, "wangui.db"), crypto)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()
	logger.Info("store ready", "data_dir", dataDir)

	// In-memory event bus. Backend components publish to it (scheduler,
	// notifier), and the admin SSE endpoint streams events to connected
	// browsers. Dies on process restart; no persistence.
	bus := events.New()

	sched := scheduler.NewMulti(st, logger)
	sched.Bus = bus
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	sched.Start(ctx)

	// Periodic session GC.
	go func() {
		t := time.NewTicker(1 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = st.GCSessions(context.Background())
			}
		}
	}()

	adminPass := os.Getenv("WANGUI_ADMIN_PASS")
	if adminPass == "" {
		logger.Warn("WANGUI_ADMIN_PASS not set — admin panel disabled")
	} else {
		logger.Info("admin panel enabled")
	}

	// Daily SQLite backup (VACUUM INTO + keep 7 days).
	bkp := &backup.Backup{
		DB:  st.DB(),
		Dir: filepath.Join(dataDir, "backups"),
		Log: logger,
	}
	bkp.Start(ctx)

	srv := &web.Server{
		Addr:      addr,
		Store:     st,
		Sched:     sched,
		Bus:       bus,
		Logger:    logger,
		SPAFS:     spaFS(),
		AdminPass: adminPass,
	}
	return srv.Run(ctx)
}

func exit(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

// setupLogger returns a slog.Logger that writes to both stdout and the configured file.
func setupLogger(cfg *config.Config) (*slog.Logger, func(), error) {
	level := slog.LevelInfo
	switch strings.ToLower(cfg.Log.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	f, err := os.OpenFile(cfg.Log.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open log: %w", err)
	}
	w := io.MultiWriter(os.Stdout, f)
	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(h), func() { _ = f.Close() }, nil
}

func mustLoad(path string) (*config.Config, *slog.Logger, func(), error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, nil, nil, err
	}
	logger, closer, err := setupLogger(cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	return cfg, logger, closer, nil
}

func runDoctor(path string) error {
	cfg, logger, closer, err := mustLoad(path)
	if err != nil {
		return err
	}
	defer closer()

	logger.Info("config loaded",
		"token", config.MaskToken(cfg.Token),
		"rule_id", cfg.RuleID,
		"lat", cfg.Location.Latitude,
		"lng", cfg.Location.Longitude,
	)

	if exp, err := jwtExp(cfg.Token); err == nil {
		remaining := time.Until(exp)
		logger.Info("token expiry", "exp", exp.Format(time.RFC3339), "remaining_h", remaining.Hours())
		if remaining < 24*time.Hour {
			logger.Warn("TOKEN expires within 24h — re-grab soon")
		}
		if remaining <= 0 {
			return fmt.Errorf("token already expired at %s", exp.Format(time.RFC3339))
		}
	} else {
		logger.Warn("failed to decode JWT exp", "err", err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c := api.New(cfg.Token)

	u, err := c.GetUser(ctx)
	if err != nil {
		return fmt.Errorf("/auth/user: %w", err)
	}
	logger.Info("auth ok",
		"name", u.UserName, "number", u.UserNumber,
		"section", u.UserSection, "class", u.UserClass)

	rules, err := c.AvailableRules(ctx)
	if err != nil {
		return fmt.Errorf("/checkin/available-rules: %w", err)
	}
	var matched *api.Rule
	for i := range rules {
		if rules[i].RuleID == cfg.RuleID {
			matched = &rules[i]
		}
		logger.Info("rule",
			"id", rules[i].RuleID, "name", rules[i].RuleName,
			"start", rules[i].StartTime, "end", rules[i].EndTime)
	}
	if matched == nil {
		return fmt.Errorf("configured rule_id=%d not in available rules", cfg.RuleID)
	}
	logger.Info("DOCTOR: all checks passed")
	return nil
}

func runStatus(path string) error {
	cfg, logger, closer, err := mustLoad(path)
	if err != nil {
		return err
	}
	defer closer()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c := api.New(cfg.Token)

	rule, err := c.CurrentRule(ctx)
	if err != nil {
		return fmt.Errorf("current rule: %w", err)
	}
	st, err := c.CheckinStatus(ctx, rule.RuleID)
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	logger.Info("status", "rule_id", rule.RuleID)
	fmt.Println(string(b))
	return nil
}

func runSign(path string) error {
	cfg, logger, closer, err := mustLoad(path)
	if err != nil {
		return err
	}
	defer closer()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := api.New(cfg.Token)

	rule, err := c.CurrentRule(ctx)
	if err != nil {
		return fmt.Errorf("current rule: %w", err)
	}
	st, err := c.CheckinStatus(ctx, rule.RuleID)
	if err != nil {
		return fmt.Errorf("pre-status: %w", err)
	}
	if st.HasCheckedIn != nil && *st.HasCheckedIn {
		logger.Info("already checked in today, abort", "msg", st.Message)
		return nil
	}
	if !st.CanCheckin {
		return fmt.Errorf("canCheckin=false: %s", st.Message)
	}

	req := api.SignRequest{
		RuleID:          rule.RuleID,
		Latitude:        cfg.Location.Latitude,
		Longitude:       cfg.Location.Longitude,
		DeviceModel:     cfg.Location.DeviceModel,
		DeviceSystem:    cfg.Location.DeviceSystem,
		LocationAddress: cfg.Location.Address,
	}
	_, err = c.Sign(ctx, req)
	if err != nil {
		if verified, verifyErr := c.CheckinStatus(ctx, rule.RuleID); verifyErr == nil &&
			verified.HasCheckedIn != nil && *verified.HasCheckedIn {
			logger.Info("SIGN OK after status verification", "rule_id", rule.RuleID)
			return nil
		}
		return err
	}
	verified, verifyErr := c.CheckinStatus(ctx, rule.RuleID)
	logger.Info("SIGN SUBMITTED", "rule_id", rule.RuleID,
		"verified", verifyErr == nil && verified.HasCheckedIn != nil && *verified.HasCheckedIn)
	return nil
}

func runDaemon(path string) error {
	cfg, logger, closer, err := mustLoad(path)
	if err != nil {
		return err
	}
	defer closer()

	logger.Info("daemon starting",
		"token", config.MaskToken(cfg.Token),
		"rule_id", cfg.RuleID,
		"primary_min", cfg.Schedule.PrimaryMinuteOffset,
		"jitter_sec", cfg.Schedule.PrimaryJitterSec,
		"retries", cfg.Schedule.RetryMinuteOffsets)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	c := api.New(cfg.Token)
	s := scheduler.New(cfg, c, notify.NewLog(logger))

	err = s.Run(ctx)
	if err != nil && err != context.Canceled {
		return err
	}
	logger.Info("daemon stopped")
	return nil
}

// jwtExp extracts the exp timestamp from an unverified JWT.
// It does NOT validate the signature — purpose is just to flag expiry early.
func jwtExp(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, fmt.Errorf("not a JWT")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, err
	}
	var p struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return time.Time{}, err
	}
	if p.Exp == 0 {
		return time.Time{}, fmt.Errorf("no exp claim")
	}
	return time.Unix(p.Exp, 0), nil
}
