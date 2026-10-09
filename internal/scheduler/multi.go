package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"wangui/internal/api"
	"wangui/internal/events"
	"wangui/internal/notify"
	"wangui/internal/store"
)

const (
	WindowHour        = 22
	WindowStartMinute = 0
	WindowEndMinute   = 30
)

// Multi is the multi-tenant scheduler.
// It wakes once per day for the 22:00–22:30 window, then fans out a goroutine
// per auto-signing user with per-user trigger/jitter/retry settings.
type Multi struct {
	store    *store.Store
	log      *slog.Logger
	notifier *notify.Dispatcher
	// Bus is the in-memory event bus. Set by main after construction; nil
	// is treated as "publishing disabled" so tests and tools that build a
	// Multi without the bus still work.
	Bus *events.Bus
}

func (m *Multi) publish(eventType string, payload any) {
	if m.Bus == nil {
		return
	}
	m.Bus.PublishJSON(eventType, payload)
}

func NewMulti(s *store.Store, l *slog.Logger) *Multi {
	return &Multi{
		store:    s,
		log:      l,
		notifier: &notify.Dispatcher{Store: s, Log: l},
	}
}

func (m *Multi) Start(ctx context.Context) {
	go m.loop(ctx)
	go m.guestCleanupLoop(ctx)
	go m.tokenWarnLoop(ctx)
	go m.rulesWatchLoop(ctx)
	go m.weeklyDigestLoop(ctx)
}

// TokenWarnThreshold is the "send warning" cutoff. Anything inside this
// window (and not yet warned for the current token) triggers a notification.
// 48h gives the user a comfortable buffer to refresh.
const TokenWarnThreshold = 48 * time.Hour

// tokenWarnLoop fires once a day at 10:00 local time, scans all users,
// and dispatches a warning email + Server酱 push for any user whose JWT
// expires within TokenWarnThreshold AND has not been warned for the
// current token cycle (token_warned_at is reset to 0 on UpdateToken).
func (m *Multi) tokenWarnLoop(ctx context.Context) {
	for {
		next := nextTokenWarnTime(time.Now())
		m.log.Info("token-warn armed", "next", next.Format(time.RFC3339))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
		m.runTokenWarnSweep(ctx)
	}
}

func nextTokenWarnTime(now time.Time) time.Time {
	today := time.Date(now.Year(), now.Month(), now.Day(), 10, 0, 0, 0, now.Location())
	if today.After(now) {
		return today
	}
	return today.Add(24 * time.Hour)
}

// rulesWatchLoop pings the school's /checkin/available-rules once a day at
// 18:00 (well outside the sign window) using any healthy user's token. If
// the rules list changed since yesterday — e.g. a holiday rule was added,
// or 22:00–22:30 became 21:30–22:00 — we cache the new snapshot and notify
// admin so they can prepare instead of getting surprised at 22:00.
func (m *Multi) rulesWatchLoop(ctx context.Context) {
	for {
		next := nextRulesWatchTime(time.Now())
		m.log.Info("rules-watch armed", "next", next.Format(time.RFC3339))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
		m.runRulesWatchSweep(ctx)
	}
}

func nextRulesWatchTime(now time.Time) time.Time {
	today := time.Date(now.Year(), now.Month(), now.Day(), 18, 0, 0, 0, now.Location())
	if today.After(now) {
		return today
	}
	return today.Add(24 * time.Hour)
}

func (m *Multi) runRulesWatchSweep(ctx context.Context) {
	users, err := m.store.ListUsers(ctx, store.UserListFilter{Limit: 50})
	if err != nil {
		m.log.Error("rules-watch list users", "err", err.Error())
		return
	}
	// Find a non-guest user with a valid token. Guests' tokens may exist
	// briefly but their accounts disappear daily; we want a stable account.
	var probe *store.User
	for _, u := range users {
		if u.IsGuest || u.IsDisabled {
			continue
		}
		if time.Until(u.TokenExp) > time.Hour {
			probe = u
			break
		}
	}
	if probe == nil {
		m.log.Warn("rules-watch: no healthy user to probe with")
		return
	}
	c := api.New(probe.Token)
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	rules, err := c.AvailableRules(probeCtx)
	if err != nil {
		m.log.Warn("rules-watch probe failed", "user", probe.UserID, "err", err.Error())
		return
	}
	// Build the new digest. We hash rule id + name + start + end + desc so
	// even a single-character description tweak triggers a notification.
	current, _ := json.Marshal(rules)
	prev, _ := m.store.GetConfig(ctx, "schoolrules.snapshot")
	if prev != "" && prev == string(current) {
		m.log.Info("rules-watch: unchanged", "rules", len(rules))
		return
	}
	if err := m.store.SetConfig(ctx, "schoolrules.snapshot", string(current)); err != nil {
		m.log.Warn("rules-watch persist failed", "err", err.Error())
	}
	if err := m.store.SetConfig(ctx, "schoolrules.updated_at",
		strconv.FormatInt(time.Now().Unix(), 10)); err != nil {
		m.log.Warn("rules-watch persist ts", "err", err.Error())
	}
	// First run on a fresh DB: persist the snapshot but skip the email —
	// there's no "previous" to diff against, so the change isn't real.
	if prev == "" {
		m.log.Info("rules-watch: initial snapshot stored", "rules", len(rules))
		return
	}
	m.log.Info("rules-watch: changed, dispatching", "rules", len(rules))
	m.notifier.DispatchRulesChanged(rules, prev, string(current))
	m.publish(events.TypeRulesChanged, map[string]any{"count": len(rules)})
}

// weeklyDigestLoop fires every Sunday at 21:00 (an hour before the sign
// window). Each active non-guest user gets a short "本周战绩" email +
// Server酱 push: how many days they signed, how many failed, and where
// they are on their streak. Admin also gets an aggregate digest so they
// see the system's overall week at a glance.
func (m *Multi) weeklyDigestLoop(ctx context.Context) {
	for {
		next := nextWeeklyDigestTime(time.Now())
		m.log.Info("weekly-digest armed", "next", next.Format(time.RFC3339))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
		m.runWeeklyDigestSweep(ctx)
	}
}

func nextWeeklyDigestTime(now time.Time) time.Time {
	// Next Sunday 21:00 local. If today is Sunday before 21:00, fire today.
	today := time.Date(now.Year(), now.Month(), now.Day(), 21, 0, 0, 0, now.Location())
	daysUntilSunday := (7 - int(now.Weekday())) % 7 // Sunday=0
	if daysUntilSunday == 0 && today.After(now) {
		return today
	}
	if daysUntilSunday == 0 {
		daysUntilSunday = 7
	}
	return today.AddDate(0, 0, daysUntilSunday)
}

func (m *Multi) runWeeklyDigestSweep(ctx context.Context) {
	users, err := m.store.ListUsers(ctx, store.UserListFilter{Limit: 500})
	if err != nil {
		m.log.Error("weekly-digest list users", "err", err.Error())
		return
	}
	now := time.Now()
	weekStart := now.AddDate(0, 0, -7)
	sent := 0
	for _, u := range users {
		if u.IsGuest || u.IsDisabled {
			continue
		}
		recs, err := m.store.ListRecordsBetween(ctx, u.UserID, weekStart, now)
		if err != nil {
			continue
		}
		stats := computeWeeklyStats(recs, weekStart, now)
		m.notifier.DispatchWeeklyDigest(u, notify.WeeklyStats{
			From:          stats.From,
			To:            stats.To,
			DaysSigned:    stats.DaysSigned,
			DaysFailed:    stats.DaysFailed,
			DaysSkipped:   stats.DaysSkipped,
			TotalAttempts: stats.TotalAttempts,
			BestDay:       stats.BestDay,
			BestStatus:    stats.BestStatus,
		})
		sent++
	}
	m.log.Info("weekly-digest sweep done", "users", sent)
}

// WeeklyStats is the per-user summary the digest email + Server酱 receive.
type WeeklyStats struct {
	From          time.Time
	To            time.Time
	DaysSigned    int    // count of distinct days with success/already/exempt
	DaysFailed    int    // distinct days with failed
	DaysSkipped   int    // distinct days with skipped (or user marked skip)
	TotalAttempts int    // raw record count
	BestDay       string // YYYY-MM-DD of fastest successful sign, "" if none
	BestStatus    string // best outcome status for BestDay
}

func computeWeeklyStats(recs []store.Record, from, to time.Time) WeeklyStats {
	s := WeeklyStats{From: from, To: to, TotalAttempts: len(recs)}
	bestByDay := map[string]string{}
	for _, r := range recs {
		key := r.OccurredAt.Format("2006-01-02")
		if prev, ok := bestByDay[key]; !ok || statusRank(r.Status) > statusRank(prev) {
			bestByDay[key] = r.Status
		}
	}
	for day, st := range bestByDay {
		switch st {
		case "success", "already", "exempt":
			s.DaysSigned++
			if s.BestDay == "" || day > s.BestDay {
				s.BestDay = day
				s.BestStatus = st
			}
		case "failed":
			s.DaysFailed++
		case "skipped":
			s.DaysSkipped++
		}
	}
	return s
}

func statusRank(st string) int {
	switch st {
	case "success":
		return 5
	case "already":
		return 4
	case "exempt":
		return 3
	case "failed":
		return 2
	case "skipped":
		return 1
	}
	return 0
}

func (m *Multi) runTokenWarnSweep(ctx context.Context) {
	users, err := m.store.ListUsers(ctx, store.UserListFilter{Limit: 500})
	if err != nil {
		m.log.Error("token-warn list users", "err", err.Error())
		return
	}
	now := time.Now()
	warned := 0
	for _, u := range users {
		if u.IsDisabled {
			continue
		}
		remaining := time.Until(u.TokenExp)
		if remaining <= 0 || remaining > TokenWarnThreshold {
			continue
		}
		// Per-token-cycle dedup: TokenWarnedAt is reset to 0 when the token
		// is rotated, so a non-zero value means we already pinged about this
		// exact token. Don't spam.
		if u.TokenWarnedAt != 0 {
			continue
		}
		hoursLeft := max(int(remaining/time.Hour), 1)
		m.notifier.DispatchTokenWarning(u, hoursLeft)
		if err := m.store.MarkTokenWarned(ctx, u.UserID, now); err != nil {
			m.log.Warn("mark token-warned", "user", u.UserID, "err", err.Error())
		}
		m.publish(events.TypeTokenWarn, map[string]any{
			"userId":    u.UserID,
			"userName":  u.UserName,
			"hoursLeft": hoursLeft,
		})
		warned++
	}
	m.log.Info("token-warn sweep done", "scanned", len(users), "warned", warned)
}

// guestCleanupLoop runs every day at 02:00 (well outside the sign window) and
// deletes guests whose expires_at is in the past. Emails admin a summary.
func (m *Multi) guestCleanupLoop(ctx context.Context) {
	for {
		next := nextCleanupTime(time.Now())
		m.log.Info("guest cleanup armed", "next", next.Format(time.RFC3339))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
		m.runGuestCleanup(ctx)
	}
}

func nextCleanupTime(now time.Time) time.Time {
	today := time.Date(now.Year(), now.Month(), now.Day(), 2, 0, 0, 0, now.Location())
	if today.After(now) {
		return today
	}
	return today.Add(24 * time.Hour)
}

func (m *Multi) runGuestCleanup(ctx context.Context) {
	expired, err := m.store.DeleteExpiredGuests(ctx, time.Now())
	if err != nil {
		m.log.Error("cleanup expired guests", "err", err.Error())
		return
	}
	if len(expired) == 0 {
		m.log.Info("cleanup: no expired guests")
		return
	}
	for _, g := range expired {
		m.log.Info("cleanup deleted guest", "user", g.UserID, "label", g.Label, "name", g.Name)
	}
	m.notifier.DispatchGuestCleanup(expired)
	m.publish(events.TypeGuestCleanup, map[string]any{"count": len(expired)})
}

func (m *Multi) loop(ctx context.Context) {
	for {
		next := nextWindowStart(time.Now())
		m.log.Info("scheduler armed", "next_window_start", next.Format(time.RFC3339))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
		m.runWindow(ctx)
	}
}

func nextWindowStart(now time.Time) time.Time {
	today := time.Date(now.Year(), now.Month(), now.Day(),
		WindowHour, WindowStartMinute, 0, 0, now.Location())
	if today.After(now) {
		return today
	}
	return today.Add(24 * time.Hour)
}

func windowEndOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(),
		WindowHour, WindowEndMinute, 0, 0, t.Location())
}

func (m *Multi) runWindow(ctx context.Context) {
	end := windowEndOf(time.Now())
	winCtx, cancel := context.WithDeadline(ctx, end.Add(30*time.Second))
	defer cancel()

	ids, err := m.store.ListAutoSignUsers(winCtx)
	if err != nil {
		m.log.Error("list auto-sign users failed", "err", err.Error())
		return
	}
	m.log.Info("window opened", "auto_sign_users", len(ids), "deadline", end.Format(time.RFC3339))
	m.publish(events.TypeWindowOpen, map[string]any{
		"users":    len(ids),
		"deadline": end.Unix(),
	})

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(uid string) {
			defer wg.Done()
			m.runForUser(winCtx, uid, end)
		}(id)
	}
	wg.Wait()
	m.log.Info("window closed")
	m.publish(events.TypeWindowClose, map[string]any{
		"users": len(ids),
		"at":    time.Now().Unix(),
	})
}

func (m *Multi) runForUser(ctx context.Context, userID string, deadline time.Time) {
	u, err := m.store.GetUser(ctx, userID)
	if err != nil {
		m.log.Error("get user failed", "user", userID, "err", err.Error())
		return
	}
	if !u.AutoSign || u.IsDisabled {
		return
	}
	// Guest users sign on specific calendar dates (sign_dates list) instead
	// of recurring weekdays (sign_days bitmask). Different schedule shape,
	// same "skip silently if not today" semantics.
	if u.IsGuest {
		if !isSignDate(u.SignDates, time.Now()) {
			m.log.Info("skip non-sign-date guest", "user", userID, "label", u.GuestLabel)
			return
		}
	} else {
		if !isSignDay(u.SignDays, time.Now()) {
			m.log.Info("skip non-sign-day", "user", userID, "sign_days", u.SignDays)
			return
		}
		// User-driven "我不在校" skip list: highest-priority opt-out.
		// Avoids the谎报位置 risk when the user is genuinely off-campus.
		if isSignDate(u.SkipDates, time.Now()) {
			m.log.Info("skip user-marked off-campus", "user", userID)
			return
		}
	}

	// Wait for trigger_minute then add jitter.
	now := time.Now()
	primary := time.Date(now.Year(), now.Month(), now.Day(),
		WindowHour, WindowStartMinute+u.TriggerMinute, 0, 0, now.Location())
	jitter := time.Duration(rand.IntN(u.JitterSec+1)) * time.Second
	target := primary.Add(jitter)
	if target.Before(now) {
		target = now.Add(jitter)
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Until(target)):
	}

	maxAttempts := 1 + u.RetryCount
	gap := time.Duration(u.RetryGapMin) * time.Minute

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if time.Now().After(deadline) {
			m.log.Warn("deadline passed", "user", userID, "attempt", attempt)
			return
		}
		// Re-read user (settings may have changed mid-window).
		cur, err := m.store.GetUser(ctx, userID)
		if err != nil {
			m.log.Error("get user (retry loop)", "user", userID, "err", err.Error())
			return
		}
		if !cur.AutoSign || cur.IsDisabled {
			return
		}
		res := m.SignOnce(ctx, cur)
		_ = m.store.AddRecord(ctx, &store.Record{
			UserID: userID, RuleID: res.RuleID,
			Status: res.Status, Message: res.Message,
		})
		m.log.Info("attempt",
			"user", userID, "attempt", attempt,
			"status", res.Status, "msg", res.Message)
		// Always publish so the admin monitor board updates in real time
		// even for transient failed attempts mid-retry.
		m.publish(events.TypeSignResult, map[string]any{
			"userId":   userID,
			"userName": cur.UserName,
			"status":   res.Status,
			"message":  res.Message,
			"attempt":  attempt,
			"terminal": res.Terminal(),
		})
		if res.Terminal() {
			// Send notification on terminal outcome (success / already / exempt).
			m.notifier.DispatchSignResult(cur, notify.SignResult{Status: res.Status, Message: res.Message})
			return
		}
		// On the FINAL failed attempt, notify once.
		if attempt == maxAttempts {
			m.notifier.DispatchSignResult(cur, notify.SignResult{Status: res.Status, Message: res.Message})
		}
		wait := gap
		if remain := time.Until(deadline); remain < wait {
			wait = remain
		}
		if wait <= 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// SignResult is the outcome of a single sign attempt.
type SignResult struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	RuleID  int    `json:"ruleId"`
}

func (r SignResult) Terminal() bool {
	switch r.Status {
	case "success", "already", "exempt":
		return true
	}
	return false
}

// SignOnce performs a single status + (optional) sign cycle for one user.
// It never writes records — the caller decides whether to persist.
func (m *Multi) SignOnce(ctx context.Context, u *store.User) SignResult {
	c := api.New(u.Token)
	rule, err := c.CurrentRule(ctx)
	if err != nil {
		if api.IsAuthExpired(err) {
			return SignResult{Status: "failed", Message: "token 已失效，请更新"}
		}
		return SignResult{Status: "failed", Message: "签到规则获取失败: " + err.Error()}
	}
	result := SignResult{RuleID: rule.RuleID}
	st, err := c.CheckinStatus(ctx, rule.RuleID)
	if err != nil {
		if api.IsAuthExpired(err) {
			result.Status, result.Message = "failed", "token 已失效，请更新"
			return result
		}
		result.Status, result.Message = "failed", "状态获取失败: "+err.Error()
		return result
	}
	if st.IsBoarding {
		result.Status, result.Message = "exempt", "外宿学生无需签到"
		return result
	}
	if st.IsExempt != nil && *st.IsExempt {
		result.Status, result.Message = "exempt", nonEmpty(st.Message, "请假中")
		return result
	}
	if st.HasCheckedIn != nil && *st.HasCheckedIn {
		result.Status, result.Message = "already", nonEmpty(st.Message, "今日已签到")
		return result
	}
	if !st.CanCheckin {
		result.Status, result.Message = "failed", nonEmpty(st.Message, "当前无法签到")
		return result
	}
	if u.Lat == 0 || u.Lng == 0 {
		result.Status, result.Message = "failed", "未配置打卡坐标"
		return result
	}
	if strings.TrimSpace(u.Address) == "" {
		result.Status, result.Message = "failed", "未配置签到地址；新版协议要求地址参与签名"
		return result
	}
	req := api.SignRequest{
		RuleID:          rule.RuleID,
		Latitude:        u.Lat,
		Longitude:       u.Lng,
		DeviceModel:     nonEmpty(u.DeviceModel, "Pixel 7"),
		DeviceSystem:    nonEmpty(u.DeviceSystem, "Android 13"),
		LocationAddress: u.Address,
	}
	if _, err := c.Sign(ctx, req); err != nil {
		// The POST response can be lost after the school has committed the
		// record. Verify before allowing the outer scheduler to retry with a
		// fresh nonce, preventing duplicate submissions.
		if verified, verifyErr := c.CheckinStatus(ctx, rule.RuleID); verifyErr == nil &&
			verified.HasCheckedIn != nil && *verified.HasCheckedIn {
			result.Status, result.Message = "success", "签到成功（状态复核通过）"
			return result
		}
		if api.IsAuthExpired(err) {
			result.Status, result.Message = "failed", "token 已失效，请更新"
			return result
		}
		var ae *api.APIError
		if errors.As(err, &ae) {
			m.log.Warn("school sign rejected",
				"user", u.UserID,
				"http_status", ae.HTTPStatus,
				"code", ae.Code,
				"message", ae.Message,
			)
			result.Status, result.Message = "failed", nonEmpty(ae.Message, "学校拒绝签到请求")
			return result
		}
		result.Status, result.Message = "failed", err.Error()
		return result
	}
	verified, err := c.CheckinStatus(ctx, rule.RuleID)
	if err == nil && verified.HasCheckedIn != nil && *verified.HasCheckedIn {
		result.Status, result.Message = "success", "签到成功（状态复核通过）"
		return result
	}
	// A successful POST is terminal even if the read model has not caught up;
	// retrying here could submit the same day's check-in twice.
	result.Status, result.Message = "success", "签到已提交，学校状态暂未同步"
	return result
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// isSignDay reports whether t falls on a weekday the user wants to sign on.
// signDays is a 7-bit mask: bit 0 = Monday … bit 6 = Sunday. The mapping
// converts Go's time.Weekday (Sunday=0..Saturday=6) into that scheme.
func isSignDay(signDays int, t time.Time) bool {
	if signDays&0x7f == 0 {
		return false
	}
	if signDays&0x7f == 0x7f {
		return true
	}
	bit := (int(t.Weekday()) + 6) % 7 // Sun→6, Mon→0, …, Sat→5
	return signDays&(1<<bit) != 0
}

// isSignDate reports whether today (in local time) is one of the JSON-encoded
// "YYYY-MM-DD" dates in the guest user's sign_dates field. Empty list or
// invalid JSON → returns false (skip).
func isSignDate(signDatesJSON string, t time.Time) bool {
	var dates []string
	if err := json.Unmarshal([]byte(signDatesJSON), &dates); err != nil {
		return false
	}
	return slices.Contains(dates, t.Format("2006-01-02"))
}
