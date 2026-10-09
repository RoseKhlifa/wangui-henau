package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"wangui/internal/store"
)

// Dispatcher is the high-level helper the scheduler/handlers use to fire
// emails. It re-reads SMTP config every Send so admin updates take effect
// immediately without restart.
type Dispatcher struct {
	Store *store.Store
	Log   *slog.Logger
}

// SignResult mirrors scheduler.SignResult (we avoid an import cycle by
// duplicating the small struct here).
type SignResult struct {
	Status  string
	Message string
}

// DispatchSignResult fans out to every channel configured for this user
// (email + Server酱) plus the admin global BCC equivalents. Non-blocking
// from the caller's perspective — fires a goroutine.
func (d *Dispatcher) DispatchSignResult(u *store.User, res SignResult) {
	go func() {
		d.dispatchSync(u, res)
		d.dispatchServerChan(u, res)
	}()
}

func (d *Dispatcher) dispatchSync(u *store.User, res SignResult) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := d.Store.GetSMTPConfig(ctx)
	if err != nil {
		d.log("smtp config load failed", "err", err.Error())
		return
	}
	if !cfg.Enabled || cfg.Host == "" || cfg.Username == "" {
		return // notifications disabled at system level
	}

	client := &EmailClient{
		Host: cfg.Host, Port: cfg.Port,
		Username: cfg.Username, Password: cfg.Password,
		From: cfg.From,
	}

	subject, text, html := renderSignEmail(u, res)

	// Guests: admin owns the relationship, the friend never gave us an
	// email. Only ship to admin BCC.
	if u.IsGuest {
		if cfg.AdminBcc != "" {
			msg := Message{
				To:      cfg.AdminBcc,
				Subject: "[临时朋友] " + subject,
				Text:    text,
				HTML:    html,
			}
			if err := client.Send(msg); err != nil {
				d.log("email to admin (guest) failed", "to", cfg.AdminBcc, "err", err.Error())
			} else {
				d.log("admin guest log email sent", "to", cfg.AdminBcc, "status", res.Status, "guest", u.GuestLabel)
			}
		}
		return
	}

	// Regular user email
	if u.NotifyEnabled && u.NotifyEmail != "" {
		msg := Message{
			To:      u.NotifyEmail,
			Subject: subject,
			Text:    text,
			HTML:    html,
		}
		// Admin bcc piggy-backs on user mail when both apply.
		if cfg.AdminBcc != "" && cfg.AdminBcc != u.NotifyEmail {
			msg.Bcc = []string{cfg.AdminBcc}
		}
		if err := client.Send(msg); err != nil {
			d.log("email to user failed", "user", u.UserID, "to", u.NotifyEmail, "err", err.Error())
		} else {
			d.log("email sent", "user", u.UserID, "to", u.NotifyEmail, "status", res.Status)
		}
		return
	}

	// User didn't opt-in, but admin still wants the log.
	if cfg.AdminBcc != "" {
		msg := Message{
			To:      cfg.AdminBcc,
			Subject: "[管理员日志] " + subject,
			Text:    text,
			HTML:    html,
		}
		if err := client.Send(msg); err != nil {
			d.log("email to admin failed", "to", cfg.AdminBcc, "err", err.Error())
		} else {
			d.log("admin log email sent", "to", cfg.AdminBcc, "status", res.Status)
		}
	}
}

// dispatchServerChan pushes the sign result through Server酱 for the user
// (if they configured + enabled their own key) and the admin (if admin
// configured + enabled the global admin key). Each channel is independent
// of the email pipeline — both can fire for the same event.
func (d *Dispatcher) dispatchServerChan(u *store.User, res SignResult) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cfg, err := d.Store.GetSMTPConfig(ctx)
	if err != nil {
		d.log("notify config load failed (serverchan)", "err", err.Error())
		return
	}

	title, body := renderSignServerChan(u, res)

	// Per-user push — guests never get one (no SendKey collected).
	if !u.IsGuest && u.ServerChanEnabled && u.ServerChanKey != "" {
		client := NewServerChan(u.ServerChanKey)
		if err := client.Send(ctx, title, body); err != nil {
			d.log("serverchan to user failed", "user", u.UserID, "err", err.Error())
		} else {
			d.log("serverchan sent to user", "user", u.UserID, "status", res.Status)
		}
	}

	// Admin push — fires for everyone (regular + guest), giving admin a single
	// stream of all sign outcomes alongside email BCC.
	if cfg.AdminServerChanEnabled && cfg.AdminServerChanKey != "" {
		adminTitle := title
		if u.IsGuest {
			adminTitle = "[临时朋友] " + title
		}
		adminBody := body
		client := NewServerChan(cfg.AdminServerChanKey)
		if err := client.Send(ctx, adminTitle, adminBody); err != nil {
			d.log("serverchan to admin failed", "err", err.Error())
		} else {
			d.log("serverchan sent to admin", "status", res.Status, "user", u.UserID)
		}
	}
}

// DispatchTokenWarning sends a "Token 即将过期" alert through every channel
// the user (and admin) has enabled. Caller should already have checked the
// expiry threshold AND token_warned_at deduping. Non-blocking.
func (d *Dispatcher) DispatchTokenWarning(u *store.User, hoursLeft int) {
	go d.dispatchTokenWarning(u, hoursLeft)
}

func (d *Dispatcher) dispatchTokenWarning(u *store.User, hoursLeft int) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, err := d.Store.GetSMTPConfig(ctx)
	if err != nil {
		d.log("token-warn smtp load failed", "err", err.Error())
		return
	}

	subject, text, html := renderTokenWarnEmail(u, hoursLeft)
	title, body := renderTokenWarnServerChan(u, hoursLeft)

	// --- Email path (user + admin BCC) ---
	if cfg.Enabled && cfg.Host != "" && cfg.Username != "" {
		client := &EmailClient{
			Host: cfg.Host, Port: cfg.Port,
			Username: cfg.Username, Password: cfg.Password, From: cfg.From,
		}
		// Guests have no user email — only admin gets notified, and even then
		// only if the guest's token actually matters (it usually doesn't, since
		// they're auto-cleaned soon). Still surface it for transparency.
		if u.IsGuest {
			if cfg.AdminBcc != "" {
				_ = client.Send(Message{
					To: cfg.AdminBcc, Subject: "[临时朋友] " + subject,
					Text: text, HTML: html,
				})
			}
		} else {
			sent := false
			if u.NotifyEnabled && u.NotifyEmail != "" {
				msg := Message{To: u.NotifyEmail, Subject: subject, Text: text, HTML: html}
				if cfg.AdminBcc != "" && cfg.AdminBcc != u.NotifyEmail {
					msg.Bcc = []string{cfg.AdminBcc}
				}
				if err := client.Send(msg); err != nil {
					d.log("token-warn email to user failed", "user", u.UserID, "err", err.Error())
				} else {
					sent = true
					d.log("token-warn email sent", "user", u.UserID, "hours_left", hoursLeft)
				}
			}
			if !sent && cfg.AdminBcc != "" {
				_ = client.Send(Message{
					To: cfg.AdminBcc, Subject: "[管理员日志] " + subject,
					Text: text, HTML: html,
				})
			}
		}
	}

	// --- Server酱 path ---
	if !u.IsGuest && u.ServerChanEnabled && u.ServerChanKey != "" {
		if err := NewServerChan(u.ServerChanKey).Send(ctx, title, body); err != nil {
			d.log("token-warn serverchan to user failed", "user", u.UserID, "err", err.Error())
		}
	}
	if cfg.AdminServerChanEnabled && cfg.AdminServerChanKey != "" {
		adminTitle := title
		if u.IsGuest {
			adminTitle = "[临时朋友] " + title
		}
		if err := NewServerChan(cfg.AdminServerChanKey).Send(ctx, adminTitle, body); err != nil {
			d.log("token-warn serverchan to admin failed", "err", err.Error())
		}
	}
}

// DispatchRulesChanged notifies admin (email + Server酱) when the school's
// /checkin/available-rules differs from the previously cached snapshot.
// The raw JSON snapshots are passed in so the message can include both
// human-readable summaries and the diff. Non-blocking.
func (d *Dispatcher) DispatchRulesChanged(rules any, prevJSON, currentJSON string) {
	go d.dispatchRulesChangedSync(rules, prevJSON, currentJSON)
}

func (d *Dispatcher) dispatchRulesChangedSync(rules any, prevJSON, currentJSON string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, err := d.Store.GetSMTPConfig(ctx)
	if err != nil {
		return
	}

	// Build a short summary listing each rule's id/name/window.
	summary := summarizeRules(rules)
	subject := "[勿外传] 学校晚归规则有变化"
	text := fmt.Sprintf("学校 /checkin/available-rules 接口返回的规则与昨天不同。\n\n%s\n\n时间：%s\n",
		summary, time.Now().Format("2006-01-02 15:04:05"))
	html := fmt.Sprintf(`<!doctype html><html><body style="font-family:-apple-system,Segoe UI,sans-serif;background:#fafafa;padding:24px;color:#18181b;">
<div style="max-width:560px;margin:0 auto;background:#fff;border:1px solid #e5e7eb;border-radius:12px;overflow:hidden;">
  <div style="padding:18px 22px;background:#3b82f6;color:#fff;">
    <div style="font-size:11px;letter-spacing:.1em;text-transform:uppercase;opacity:.85;">勿外传 · 学校规则变化</div>
    <div style="font-size:20px;font-weight:700;margin-top:4px;">规则有变化</div>
  </div>
  <div style="padding:18px 22px;font-size:14px;line-height:1.7;color:#3f3f46;">
    <p>学校晚归系统返回的可用规则与上次记录不同：</p>
    <pre style="background:#f4f4f5;padding:10px 12px;border-radius:8px;font-size:12px;line-height:1.5;white-space:pre-wrap;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;">%s</pre>
  </div>
  <div style="padding:12px 22px;font-size:11px;color:#a1a1aa;background:#fafafa;border-top:1px solid #e5e7eb;">
    勿外传 · 仅内部使用 · 自动发送，请勿回复
  </div>
</div></body></html>`, summary)

	if cfg.Enabled && cfg.Host != "" && cfg.Username != "" && cfg.AdminBcc != "" {
		client := &EmailClient{Host: cfg.Host, Port: cfg.Port, Username: cfg.Username, Password: cfg.Password, From: cfg.From}
		if err := client.Send(Message{To: cfg.AdminBcc, Subject: subject, Text: text, HTML: html}); err != nil {
			d.log("rules-changed email failed", "err", err.Error())
		}
	}
	if cfg.AdminServerChanEnabled && cfg.AdminServerChanKey != "" {
		body := "学校 /checkin/available-rules 接口返回的规则与上次不同：\n\n" + summary
		if err := NewServerChan(cfg.AdminServerChanKey).Send(ctx, "⚠️ 学校晚归规则有变化", body); err != nil {
			d.log("rules-changed serverchan failed", "err", err.Error())
		}
	}
	_ = prevJSON
	_ = currentJSON
}

func summarizeRules(rules any) string {
	// Best-effort: rules is []api.Rule but we accept any to avoid import cycle.
	// Marshal-then-unmarshal to a local struct so we can format it nicely.
	type rule struct {
		RuleID      int    `json:"ruleId"`
		RuleName    string `json:"ruleName"`
		StartTime   string `json:"startTime"`
		EndTime     string `json:"endTime"`
		Description string `json:"description"`
	}
	raw, _ := json.Marshal(rules)
	var rs []rule
	_ = json.Unmarshal(raw, &rs)
	if len(rs) == 0 {
		return "(空规则列表)"
	}
	var b strings.Builder
	for i, r := range rs {
		fmt.Fprintf(&b, "%d. [#%d] %s\n   时段：%s – %s\n",
			i+1, r.RuleID, nonBlank(r.RuleName, "(无名)"), r.StartTime, r.EndTime)
		if r.Description != "" {
			fmt.Fprintf(&b, "   说明：%s\n", r.Description)
		}
	}
	return b.String()
}

// DispatchGuestCleanup sends one summary email to admin listing the guests
// the cleanup ticker just deleted. Non-blocking.
func (d *Dispatcher) DispatchGuestCleanup(expired []store.ExpiredGuest) {
	if len(expired) == 0 {
		return
	}
	go d.dispatchCleanupSync(expired)
}

func (d *Dispatcher) dispatchCleanupSync(expired []store.ExpiredGuest) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := d.Store.GetSMTPConfig(ctx)
	if err != nil || !cfg.Enabled || cfg.Host == "" || cfg.AdminBcc == "" {
		return
	}
	client := &EmailClient{
		Host: cfg.Host, Port: cfg.Port,
		Username: cfg.Username, Password: cfg.Password,
		From: cfg.From,
	}

	var b strings.Builder
	for _, g := range expired {
		fmt.Fprintf(&b, "· %s (%s · %s)\n", g.Label, g.Name, g.UserID)
	}
	subject := fmt.Sprintf("[勿外传] 已自动清理 %d 个过期临时朋友", len(expired))
	text := fmt.Sprintf("清理时间：%s\n\n%s", time.Now().Format("2006-01-02 15:04"), b.String())
	if err := client.Send(Message{
		To:      cfg.AdminBcc,
		Subject: subject,
		Text:    text,
	}); err != nil {
		d.log("guest cleanup email failed", "err", err.Error())
	} else {
		d.log("guest cleanup email sent", "to", cfg.AdminBcc, "count", len(expired))
	}
}

func renderSignEmail(u *store.User, res SignResult) (subject, text, html string) {
	statusLabels := map[string]string{
		"success": "签到成功",
		"already": "今日已签",
		"exempt":  "免签",
		"failed":  "签到失败",
		"skipped": "跳过",
	}
	statusColor := map[string]string{
		"success": "#10b981",
		"already": "#3b82f6",
		"exempt":  "#71717a",
		"failed":  "#ef4444",
		"skipped": "#f59e0b",
	}
	label, ok := statusLabels[res.Status]
	if !ok {
		label = res.Status
	}
	color := statusColor[res.Status]
	if color == "" {
		color = "#71717a"
	}
	when := time.Now().Format("2006-01-02 15:04:05")
	tagline := taglineForSign(res.Status)

	subject = fmt.Sprintf("[勿外传] %s · %s", label, u.UserName)
	text = fmt.Sprintf("姓名：%s\n学号：%s\n结果：%s\n说明：%s\n时间：%s\n",
		u.UserName, u.UserNumber, label, res.Message, when)
	if tagline != "" {
		text = text + "\n— " + tagline + "\n"
	}
	// Build the tagline HTML node only when we have one, so the email
	// doesn't have a hanging empty <em> for skipped/unknown statuses.
	taglineHTML := ""
	if tagline != "" {
		taglineHTML = fmt.Sprintf(
			`<div style="margin-top:14px;padding-top:12px;border-top:1px dashed #e5e7eb;font-size:12px;color:#71717a;font-style:italic;">— %s</div>`,
			tagline,
		)
	}
	html = fmt.Sprintf(`<!doctype html><html><body style="font-family: -apple-system,Segoe UI,sans-serif; background:#fafafa; padding:24px; color:#18181b;">
<div style="max-width:560px;margin:0 auto;background:#fff;border:1px solid #e5e7eb;border-radius:12px;overflow:hidden;">
  <div style="padding:18px 22px;background:%s;color:#fff;">
    <div style="font-size:11px;letter-spacing:.1em;text-transform:uppercase;opacity:.8;">勿外传 · 签到结果</div>
    <div style="font-size:22px;font-weight:700;margin-top:4px;">%s</div>
  </div>
  <div style="padding:18px 22px;font-size:14px;line-height:1.7;">
    <div><strong style="color:#71717a;">姓名</strong>　%s</div>
    <div><strong style="color:#71717a;">学号</strong>　%s</div>
    <div><strong style="color:#71717a;">时间</strong>　%s</div>
    <div style="margin-top:10px;padding:10px 12px;background:#f4f4f5;border-radius:8px;font-size:13px;color:#3f3f46;">%s</div>
    %s
  </div>
  <div style="padding:12px 22px;font-size:11px;color:#a1a1aa;background:#fafafa;border-top:1px solid #e5e7eb;">
    勿外传 · 仅内部使用 · 自动发送，请勿回复
  </div>
</div>
</body></html>`,
		color, label, u.UserName, u.UserNumber, when, res.Message, taglineHTML)
	return
}

// renderSignServerChan produces a compact Server酱 push for one sign result.
// title is what shows in the wechat notification banner; body is the
// in-detail markdown.
func renderSignServerChan(u *store.User, res SignResult) (title, body string) {
	statusLabels := map[string]string{
		"success": "✅ 签到成功",
		"already": "🔵 今日已签",
		"exempt":  "💤 免签",
		"failed":  "❌ 签到失败",
		"skipped": "⏭ 跳过",
	}
	label, ok := statusLabels[res.Status]
	if !ok {
		label = res.Status
	}
	when := time.Now().Format("2006-01-02 15:04:05")
	if u.IsGuest {
		title = fmt.Sprintf("%s · %s", label, u.GuestLabel)
	} else {
		title = fmt.Sprintf("%s · %s", label, u.UserName)
	}
	body = fmt.Sprintf(
		"**结果**：%s\n\n**姓名**：%s\n\n**学号**：`%s`\n\n**说明**：%s\n\n**时间**：%s",
		label, u.UserName, u.UserNumber, nonBlank(res.Message, "—"), when,
	)
	body = withTagline(body, taglineForSign(res.Status))
	return
}

// renderTokenWarnEmail returns subject + text + html for a token-expiry alert.
func renderTokenWarnEmail(u *store.User, hoursLeft int) (subject, text, html string) {
	human := humanHours(hoursLeft)
	subject = fmt.Sprintf("[勿外传] Token 即将过期 · %s · 剩 %s", u.UserName, human)
	text = fmt.Sprintf(
		"姓名：%s\n学号：%s\n剩余：%s\n到期时间：%s\n\n请尽快打开 wangui 的「账号」页，在电脑微信重新授权并刷新 Token。\n",
		u.UserName, u.UserNumber, human, u.TokenExp.Format("2006-01-02 15:04"),
	)
	html = fmt.Sprintf(`<!doctype html><html><body style="font-family:-apple-system,Segoe UI,sans-serif;background:#fafafa;padding:24px;color:#18181b;">
<div style="max-width:560px;margin:0 auto;background:#fff;border:1px solid #e5e7eb;border-radius:12px;overflow:hidden;">
  <div style="padding:18px 22px;background:#f59e0b;color:#fff;">
    <div style="font-size:11px;letter-spacing:.1em;text-transform:uppercase;opacity:.85;">勿外传 · Token 提醒</div>
    <div style="font-size:22px;font-weight:700;margin-top:4px;">Token 即将过期</div>
  </div>
  <div style="padding:18px 22px;font-size:14px;line-height:1.7;">
    <div><strong style="color:#71717a;">姓名</strong>　%s</div>
    <div><strong style="color:#71717a;">学号</strong>　%s</div>
    <div><strong style="color:#71717a;">剩余</strong>　<span style="color:#d97706;font-weight:600;">%s</span></div>
    <div><strong style="color:#71717a;">到期</strong>　%s</div>
    <div style="margin-top:14px;padding:12px;background:#fffbeb;border:1px solid #fde68a;border-radius:8px;font-size:13px;color:#92400e;">
      请尽快打开 wangui 站点，进入「账号」页，在电脑微信重新授权并刷新 Token，否则到期后将无法自动签到。
    </div>
  </div>
  <div style="padding:12px 22px;font-size:11px;color:#a1a1aa;background:#fafafa;border-top:1px solid #e5e7eb;">
    勿外传 · 仅内部使用 · 自动发送，请勿回复
  </div>
</div></body></html>`,
		u.UserName, u.UserNumber, human, u.TokenExp.Format("2006-01-02 15:04"))
	return
}

func renderTokenWarnServerChan(u *store.User, hoursLeft int) (title, body string) {
	human := humanHours(hoursLeft)
	title = fmt.Sprintf("⚠️ Token 即将过期 · %s · 剩 %s", u.UserName, human)
	body = fmt.Sprintf(
		"**姓名**：%s\n\n**学号**：`%s`\n\n**剩余**：%s\n\n**到期**：%s\n\n请尽快打开 wangui 「账号」页，在电脑微信重新授权并刷新 Token，否则将无法自动签到。",
		u.UserName, u.UserNumber, human, u.TokenExp.Format("2006-01-02 15:04"),
	)
	body = withTagline(body, taglineForTokenWarn())
	return
}

func humanHours(h int) string {
	if h <= 0 {
		return "已过期"
	}
	if h < 24 {
		return fmt.Sprintf("%d 小时", h)
	}
	d := h / 24
	rem := h % 24
	if rem == 0 {
		return fmt.Sprintf("%d 天", d)
	}
	return fmt.Sprintf("%d 天 %d 小时", d, rem)
}

func nonBlank(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func (d *Dispatcher) log(msg string, kv ...any) {
	if d.Log == nil {
		return
	}
	d.Log.Info("[notify] "+msg, kv...)
}
