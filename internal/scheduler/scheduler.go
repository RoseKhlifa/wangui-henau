package scheduler

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"wangui/internal/api"
	"wangui/internal/config"
	"wangui/internal/notify"
)

// Scheduler drives the daily signing cadence for one tenant.
type Scheduler struct {
	cfg    *config.Config
	client *api.Client
	n      notify.Notifier
	rng    *rand.Rand
}

func New(cfg *config.Config, client *api.Client, n notify.Notifier) *Scheduler {
	return &Scheduler{
		cfg:    cfg,
		client: client,
		n:      n,
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Run blocks until ctx is canceled, signing once per day inside the window.
func (s *Scheduler) Run(ctx context.Context) error {
	for {
		next := s.nextPrimaryFire(time.Now())
		s.n.Info("scheduler armed", "next_primary", next.Format(time.RFC3339))

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Until(next)):
		}

		s.runDay(ctx)
	}
}

// nextPrimaryFire returns the next 22:(primary_minute_offset)+jitter local time after `from`.
func (s *Scheduler) nextPrimaryFire(from time.Time) time.Time {
	offset := s.cfg.Schedule.PrimaryMinuteOffset
	jitter := s.rng.Intn(s.cfg.Schedule.PrimaryJitterSec + 1)

	d := from
	primary := time.Date(d.Year(), d.Month(), d.Day(), 22, offset, jitter, 0, d.Location())
	if !primary.After(from) {
		primary = primary.Add(24 * time.Hour)
	}
	return primary
}

// runDay attempts the primary sign + retries until success / window closes.
func (s *Scheduler) runDay(ctx context.Context) {
	// Primary attempt.
	if s.attempt(ctx, "primary") {
		return
	}
	// Retries at fixed minute offsets within the window.
	today := time.Now()
	for _, off := range s.cfg.Schedule.RetryMinuteOffsets {
		retryAt := time.Date(today.Year(), today.Month(), today.Day(), 22, off, 0, 0, today.Location())
		if !retryAt.After(time.Now()) {
			s.n.Warn("retry slot already past, skip", "offset_min", off)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(retryAt)):
		}
		if s.attempt(ctx, fmt.Sprintf("retry-22:%02d", off)) {
			return
		}
	}
	s.n.Error("all attempts failed for today")
}

// attempt does a single status + sign cycle. Returns true on success or harmless skip.
func (s *Scheduler) attempt(ctx context.Context, label string) bool {
	s.n.Info("attempt start", "label", label)

	rule, err := s.client.CurrentRule(ctx)
	if err != nil {
		s.n.Error("rule fetch failed", "label", label, "err", err.Error())
		if api.IsAuthExpired(err) {
			s.n.Error("TOKEN EXPIRED — manual refresh required")
		}
		return false
	}
	st, err := s.client.CheckinStatus(ctx, rule.RuleID)
	if err != nil {
		s.n.Error("status fetch failed", "label", label, "err", err.Error())
		if api.IsAuthExpired(err) {
			s.n.Error("TOKEN EXPIRED — manual refresh required")
		}
		return false
	}
	if st.IsBoarding {
		s.n.Info("user is boarding, skip", "msg", st.Message)
		return true
	}
	if st.IsExempt != nil && *st.IsExempt {
		s.n.Info("user is on leave, skip", "msg", st.Message)
		return true
	}
	if st.HasCheckedIn != nil && *st.HasCheckedIn {
		s.n.Info("already checked in, skip", "msg", st.Message)
		return true
	}
	if !st.CanCheckin {
		s.n.Warn("canCheckin=false", "msg", st.Message)
		return false
	}

	req := api.SignRequest{
		RuleID:          rule.RuleID,
		Latitude:        s.cfg.Location.Latitude,
		Longitude:       s.cfg.Location.Longitude,
		DeviceModel:     s.cfg.Location.DeviceModel,
		DeviceSystem:    s.cfg.Location.DeviceSystem,
		LocationAddress: s.cfg.Location.Address,
	}
	if _, err := s.client.Sign(ctx, req); err != nil {
		if verified, verifyErr := s.client.CheckinStatus(ctx, rule.RuleID); verifyErr == nil &&
			verified.HasCheckedIn != nil && *verified.HasCheckedIn {
			s.n.Info("SIGN OK after status verification", "label", label, "rule_id", rule.RuleID)
			return true
		}
		s.n.Error("sign failed", "label", label, "err", err.Error())
		return false
	}
	verified, verifyErr := s.client.CheckinStatus(ctx, rule.RuleID)
	if verifyErr == nil && verified.HasCheckedIn != nil && *verified.HasCheckedIn {
		s.n.Info("SIGN OK", "label", label, "rule_id", rule.RuleID, "verified", true)
	} else {
		s.n.Info("SIGN SUBMITTED", "label", label, "rule_id", rule.RuleID, "verified", false)
	}
	return true
}

// ErrWindowClosed indicates we passed 22:30 without success (unused for now, hooked later).
var ErrWindowClosed = errors.New("checkin window closed")
