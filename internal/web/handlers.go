package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"wangui/internal/api"
	"wangui/internal/events"
	"wangui/internal/notify"
	"wangui/internal/scheduler"
	"wangui/internal/store"
)

var pinPattern = regexp.MustCompile(`^\d{4,6}$`)

func validPin(s string) bool { return pinPattern.MatchString(s) }

type handlers struct {
	store       *store.Store
	sched       *scheduler.Multi
	bus         *events.Bus
	log         *slog.Logger
	adminPass   string // plain text from env; empty means admin disabled
	schoolOAuth *schoolOAuthManager

	loginLimiter *rateLimiter // per-IP rate limit for POST /login (and /activate)
}

// ---------- POST /api/v1/login ----------
// Lightweight returning-user login: 学号 + PIN. Rate-limited by IP.

type loginReq struct {
	UserNumber string `json:"userNumber"`
	Pin        string `json:"pin"`
}

func (h *handlers) login(w http.ResponseWriter, r *http.Request) {
	if !h.loginLimiter.allow(clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "尝试过于频繁，请稍后再试")
		return
	}
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	number := strings.TrimSpace(req.UserNumber)
	pin := strings.TrimSpace(req.Pin)
	if number == "" || pin == "" {
		writeErr(w, http.StatusBadRequest, "学号和 PIN 都不能为空")
		return
	}
	// Always do a bcrypt compare to mask user existence (constant-time-ish).
	u, err := h.store.FindByNumber(r.Context(), number)
	const genericErr = "学号或 PIN 不正确，或账号尚未激活"
	if err != nil {
		// Burn a hash comparison anyway so timing doesn't leak enumeration.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$abcdefghijklmnopqrstuv.dummyhashforsidechanneldefense"), []byte(pin))
		writeErr(w, http.StatusUnauthorized, genericErr)
		return
	}
	if len(u.PinHash) == 0 {
		writeErr(w, http.StatusUnauthorized, "该账号未设置 PIN，请通过「激活」流程重新激活并设置")
		return
	}
	if err := bcrypt.CompareHashAndPassword(u.PinHash, []byte(pin)); err != nil {
		writeErr(w, http.StatusUnauthorized, genericErr)
		return
	}
	if u.IsDisabled {
		writeErr(w, http.StatusForbidden, "该账号已被禁用，请联系管理员")
		return
	}
	sess, err := h.store.CreateSession(r.Context(), u.UserID, false, 30*24*time.Hour)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "会话创建失败")
		return
	}
	setSessionCookie(w, sessionCookie, sess.SessionID, sess.ExpiresAt)
	h.log.Info("login ok", "user", u.UserID, "name", u.UserName)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		if i := strings.IndexByte(ip, ','); i >= 0 {
			return strings.TrimSpace(ip[:i])
		}
		return strings.TrimSpace(ip)
	}
	// RemoteAddr includes the source port — strip it so all requests from
	// the same IP share one rate-limit bucket.
	if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return ip
	}
	return r.RemoteAddr
}

// ---------- POST /api/v1/activate/precheck ----------
//
// Lightweight invite-code probe used by the two-step activation flow. Lets
// us gate the wechat-OAuth UI behind a valid code so a random visitor with
// no invite never sees the QR / "copy callback URL" instructions. NOT a
// full activate — just answers "is this code redeemable?".
//
// Rate-limited by IP (shares the login bucket) so an attacker can't brute
// the invite-code space.

type activatePrecheckReq struct {
	InviteCode string `json:"inviteCode"`
}

func (h *handlers) activatePrecheck(w http.ResponseWriter, r *http.Request) {
	if !h.loginLimiter.allow(clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "尝试过于频繁，请稍后再试")
		return
	}
	var req activatePrecheckReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	code := strings.TrimSpace(strings.ToUpper(req.InviteCode))
	if code == "" {
		writeErr(w, http.StatusBadRequest, "邀请码不能为空")
		return
	}
	c, err := h.store.GetCode(r.Context(), code)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "邀请码不存在")
		return
	}
	if c.Disabled {
		writeErr(w, http.StatusBadRequest, "该邀请码已被禁用")
		return
	}
	// We can't tell here whether the caller is the original binder (no token
	// yet, so we don't know their user_id). If the code is bound, we let
	// them through with a heads-up. The real check happens in /activate,
	// which compares the school-returned user_id to the existing binding.
	if c.BoundUserID != nil && *c.BoundUserID != "" {
		// A bound code may still be reactivated by its original owner. The
		// identity check remains in /activate after OAuth completes.
	}
	prepared, err := h.schoolOAuth.prepare(r.Context(), activationOAuthAudience(code))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	out := map[string]any{
		"ok":               true,
		"attemptId":        prepared.AttemptID,
		"authorizationUrl": prepared.AuthorizationURL,
		"expiresAt":        prepared.ExpiresAt,
	}
	if c.BoundUserID != nil && *c.BoundUserID != "" {
		out["note"] = "该邀请码已被使用过。如你是原激活人想重新激活，可以继续；否则将被拒绝。"
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------- POST /api/v1/activate ----------
// First-time registration: invite code + school JWT + disclaimer. Rate-limited by IP.

type activateReq struct {
	schoolAuthInput
	InviteCode         string `json:"inviteCode"`
	Pin                string `json:"pin"`
	DisclaimerAccepted bool   `json:"disclaimerAccepted"`
}

func (h *handlers) activate(w http.ResponseWriter, r *http.Request) {
	if !h.loginLimiter.allow(clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "尝试过于频繁，请稍后再试")
		return
	}
	var req activateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if !req.DisclaimerAccepted {
		writeErr(w, http.StatusBadRequest, "必须先同意免责声明")
		return
	}
	code := strings.TrimSpace(strings.ToUpper(req.InviteCode))
	if code == "" {
		writeErr(w, http.StatusBadRequest, "邀请码不能为空")
		return
	}
	pin := strings.TrimSpace(req.Pin)
	if !validPin(pin) {
		writeErr(w, http.StatusBadRequest, "PIN 必须是 4–6 位数字")
		return
	}
	pinHash, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "PIN 哈希失败")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	auth, status, err := h.resolveSchoolAuth(ctx, req.schoolAuthInput, activationOAuthAudience(code))
	if err != nil {
		writeErr(w, status, err.Error())
		return
	}

	// Guard: if this user already has a different invite code bound, refuse.
	// They must delete their account first to free the old code, then activate with the new one.
	var existingUser *store.User
	if existing, err := h.store.GetUser(ctx, auth.Claims.Iss); err == nil {
		existingUser = existing
		if existing.InviteCode != "" && existing.InviteCode != code {
			writeErr(w, http.StatusForbidden,
				"该学号已绑定邀请码 "+existing.InviteCode+"，不能再激活新邀请码。如需更换请先在「账号」页注销。")
			return
		}
	}
	if !auth.ProfileHydrated && existingUser != nil {
		auth.User = &api.User{
			UserName:      existingUser.UserName,
			UserNumber:    existingUser.UserNumber,
			UserSection:   existingUser.UserSection,
			UserClass:     existingUser.UserClass,
			UserAvatarURL: existingUser.UserAvatarURL,
		}
	}

	// Atomically bind the invite code to this user_id.
	if _, err := h.store.BindCode(ctx, code, auth.Claims.Iss); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusBadRequest, "邀请码不存在")
			return
		}
		if errors.Is(err, store.ErrConflict) {
			writeErr(w, http.StatusForbidden, err.Error())
			return
		}
		h.log.Error("bind code failed", "err", err.Error())
		writeErr(w, http.StatusInternalServerError, "邀请码绑定失败")
		return
	}

	// Per-user "personal sign minute" so users don't all fire at 22:02.
	// Range is intentionally narrow: 0..9 → every user signs within
	// 22:00–22:10. That leaves the user 20 minutes (until 22:30) to bail
	// out to a manual sign-in from a teaching building if our auto-sign
	// silently fails. Trade-off: 10 users squeezed into 10 minutes will
	// occasionally pick the same minute, but the 60-second jitter spreads
	// them across seconds so they don't fire simultaneously.
	//
	// We only set these on first activation; re-activation (existing user)
	// keeps whatever they had configured. UpsertUser only updates identity
	// + token fields on conflict, so the schedule columns stay untouched.
	u := &store.User{
		UserID:        auth.Claims.Iss,
		UserName:      auth.User.UserName,
		UserNumber:    auth.User.UserNumber,
		UserSection:   auth.User.UserSection,
		UserClass:     auth.User.UserClass,
		UserAvatarURL: auth.User.UserAvatarURL,
		Token:         auth.Token,
		TokenExp:      auth.Claims.ExpiresAt(),
		AutoSign:      true,
		InviteCode:    code,
		DeviceModel:   "Pixel 7",
		DeviceSystem:  "Android 13",
		TriggerMinute: rand.IntN(10),
		JitterSec:     60,
		PinHash:       pinHash,
	}
	if err := h.store.UpsertUser(ctx, u); err != nil {
		h.log.Error("upsert user failed", "err", err.Error())
		writeErr(w, http.StatusInternalServerError, "保存失败")
		return
	}
	// Ensure invite_code is set on a re-activation as well.
	_ = h.store.SetInviteCode(ctx, auth.Claims.Iss, code)

	sess, err := h.store.CreateSession(ctx, auth.Claims.Iss, false, 30*24*time.Hour)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "会话创建失败")
		return
	}
	setSessionCookie(w, sessionCookie, sess.SessionID, sess.ExpiresAt)
	h.log.Info("activate ok", "user", auth.Claims.Iss, "code", code, "name", auth.User.UserName)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- GET /api/v1/me ----------

func (h *handlers) me(w http.ResponseWriter, r *http.Request) {
	u, err := h.store.GetUser(r.Context(), userIDOf(r))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "用户不存在")
			return
		}
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	dto := meDTO(u)
	// Enrich settings with dorm name (for display) when bound.
	if u.DormID != nil {
		if d, err := h.store.GetDorm(r.Context(), *u.DormID); err == nil {
			if settings, ok := dto["settings"].(map[string]any); ok {
				settings["dormName"] = d.Name
			}
		}
	}
	writeJSON(w, http.StatusOK, dto)
}

func meDTO(u *store.User) map[string]any {
	remaining := time.Until(u.TokenExp)
	if remaining < 0 {
		remaining = 0
	}
	return map[string]any{
		"userId":        u.UserID,
		"userName":      u.UserName,
		"userNumber":    u.UserNumber,
		"userSection":   u.UserSection,
		"userClass":     u.UserClass,
		"userAvatarUrl": u.UserAvatarURL,
		"inviteCode":    u.InviteCode,
		"isDisabled":    u.IsDisabled,
		"hasPin":        len(u.PinHash) > 0,
		"token": map[string]any{
			"expiresAt":    u.TokenExp.Unix(),
			"validUntil":   u.TokenExp.Format(time.RFC3339),
			"remainingSec": int64(remaining.Seconds()),
			"isValid":      time.Now().Before(u.TokenExp),
		},
		"settings": settingsDTO(u),
	}
}

func settingsDTO(u *store.User) map[string]any {
	skipDates := []string{}
	_ = json.Unmarshal([]byte(u.SkipDates), &skipDates)
	return map[string]any{
		"autoSign":          u.AutoSign,
		"dormId":            u.DormID,
		"latitude":          u.Lat,
		"longitude":         u.Lng,
		"address":           u.Address,
		"city":              u.City,
		"road":              u.Road,
		"poi":               u.Poi,
		"deviceModel":       u.DeviceModel,
		"deviceSystem":      u.DeviceSystem,
		"triggerMinute":     u.TriggerMinute,
		"jitterSec":         u.JitterSec,
		"retryCount":        u.RetryCount,
		"retryGapMin":       u.RetryGapMin,
		"savedLocations":    json.RawMessage(u.SavedLocations),
		"notifyEmail":       u.NotifyEmail,
		"notifyEnabled":     u.NotifyEnabled,
		"signDays":          u.SignDays,
		"serverChanKeySet":  u.ServerChanKey != "",
		"serverChanEnabled": u.ServerChanEnabled,
		"skipDates":         skipDates,
	}
}

// POST /api/v1/skip-today — toggle today's date in the user's skip_dates
// JSON list. Idempotent: calling it twice in a row enables then disables.
// Body optional; if absent, defaults to today.
//
// Why a dedicated endpoint instead of going through /settings: the action
// is "skip just tonight" — a single button on Dashboard. Wrapping it in
// the full settings PUT makes the UI weird, and we get a chance to clean
// up past skip dates while we're at it.
func (h *handlers) skipToday(w http.ResponseWriter, r *http.Request) {
	uid := userIDOf(r)
	u, err := h.store.GetUser(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusNotFound, "用户不存在")
		return
	}
	var req struct {
		Date string `json:"date"` // optional; default today
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	loc := time.Now().Location()
	target := time.Now().In(loc).Format("2006-01-02")
	if req.Date != "" {
		if _, err := time.ParseInLocation("2006-01-02", req.Date, loc); err == nil {
			target = req.Date
		}
	}

	var dates []string
	_ = json.Unmarshal([]byte(u.SkipDates), &dates)
	// Drop entries older than today — keeps the list bounded.
	today := time.Now().In(loc).Format("2006-01-02")
	pruned := make([]string, 0, len(dates))
	toggledOff := false
	for _, d := range dates {
		if d < today {
			continue // expired
		}
		if d == target {
			toggledOff = true
			continue // toggle removes
		}
		pruned = append(pruned, d)
	}
	action := "skipped"
	if !toggledOff {
		pruned = append(pruned, target)
		action = "skipped"
	} else {
		action = "unskipped"
	}
	raw, _ := json.Marshal(pruned)
	if err := h.store.UpdateSkipDates(r.Context(), uid, string(raw)); err != nil {
		writeErr(w, http.StatusInternalServerError, "保存失败")
		return
	}
	h.log.Info("skip-date toggled", "user", uid, "date", target, "action", action)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"skipDates": pruned,
		"toggled":   target,
		"action":    action,
	})
}

// ---------- GET /api/v1/dorms ----------
// Public-ish list (auth required): a user sees what dorms admin has curated.
func (h *handlers) listDorms(w http.ResponseWriter, r *http.Request) {
	dorms, err := h.store.ListDorms(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	out := make([]map[string]any, 0, len(dorms))
	for _, d := range dorms {
		out = append(out, map[string]any{
			"id":                d.ID,
			"name":              d.Name,
			"latitude":          d.Latitude,
			"longitude":         d.Longitude,
			"address":           d.Address,
			"city":              d.City,
			"road":              d.Road,
			"poi":               d.Poi,
			"note":              d.Note,
			"sendAddressFields": d.SendAddressFields,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------- PUT /api/v1/token ----------

func (h *handlers) updateToken(w http.ResponseWriter, r *http.Request) {
	uid := userIDOf(r)
	var req schoolAuthInput
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	auth, status, err := h.resolveSchoolAuth(ctx, req, userOAuthAudience(uid))
	if err != nil {
		writeErr(w, status, err.Error())
		return
	}
	if auth.Claims.Iss != uid {
		writeErr(w, http.StatusBadRequest, "Token 属于另一个账号，无法覆盖")
		return
	}
	if err := h.store.UpdateToken(ctx, uid, auth.Token, auth.Claims.ExpiresAt()); err != nil {
		writeErr(w, http.StatusInternalServerError, "保存失败")
		return
	}
	// Also refresh display fields from /auth/user. If the user's avatar
	// fetch failed during activation (school CDN flake, transient 403),
	// re-grabbing the token now also re-grabs the avatar — letting users
	// self-heal a broken avatar without admin intervention.
	if auth.ProfileHydrated {
		if err := h.store.UpdateUserProfile(ctx, uid,
			auth.User.UserName, auth.User.UserNumber,
			auth.User.UserSection, auth.User.UserClass,
			auth.User.UserAvatarURL,
		); err != nil {
			h.log.Warn("refresh profile failed (token still updated)", "user", uid, "err", err.Error())
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "expiresAt": auth.Claims.Exp,
	})
}

// ---------- GET / PUT /api/v1/settings ----------

func (h *handlers) getSettings(w http.ResponseWriter, r *http.Request) {
	u, err := h.store.GetUser(r.Context(), userIDOf(r))
	if err != nil {
		writeErr(w, http.StatusNotFound, "用户不存在")
		return
	}
	writeJSON(w, http.StatusOK, settingsDTO(u))
}

type updateSettingsReq struct {
	AutoSign          *bool   `json:"autoSign"`
	DormID            *int64  `json:"dormId"`
	DeviceModel       *string `json:"deviceModel"`
	DeviceSystem      *string `json:"deviceSystem"`
	TriggerMinute     *int    `json:"triggerMinute"`
	JitterSec         *int    `json:"jitterSec"`
	RetryCount        *int    `json:"retryCount"`
	RetryGapMin       *int    `json:"retryGapMin"`
	NotifyEmail       *string `json:"notifyEmail"`
	NotifyEnabled     *bool   `json:"notifyEnabled"`
	ServerChanKey     *string `json:"serverChanKey"`
	ServerChanEnabled *bool   `json:"serverChanEnabled"`
	SignDays          *int    `json:"signDays"`
	// Legacy raw-coord fields, accepted for backwards compat / admin override.
	Latitude       *float64         `json:"latitude"`
	Longitude      *float64         `json:"longitude"`
	Address        *string          `json:"address"`
	City           *string          `json:"city"`
	Road           *string          `json:"road"`
	Poi            *string          `json:"poi"`
	SavedLocations *json.RawMessage `json:"savedLocations"`
}

func (h *handlers) updateSettings(w http.ResponseWriter, r *http.Request) {
	uid := userIDOf(r)
	var req updateSettingsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	u, err := h.store.GetUser(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusNotFound, "用户不存在")
		return
	}
	if req.AutoSign != nil {
		u.AutoSign = *req.AutoSign
	}
	// Dorm selection takes precedence over manual coords: looking up the dorm
	// and snapshotting its coords into the user record.
	if req.DormID != nil {
		if *req.DormID == 0 {
			// dormId=0 means unbind.
			_ = h.store.SetUserDorm(r.Context(), uid, nil)
			u.DormID = nil
			u.Lat, u.Lng = 0, 0
			u.Address, u.City, u.Road, u.Poi = "", "", "", ""
		} else {
			dorm, err := h.store.GetDorm(r.Context(), *req.DormID)
			if err != nil {
				writeErr(w, http.StatusBadRequest, "宿舍楼不存在")
				return
			}
			if err := h.store.SetUserDorm(r.Context(), uid, dorm); err != nil {
				writeErr(w, http.StatusInternalServerError, "保存失败")
				return
			}
			u.DormID = &dorm.ID
			u.Lat, u.Lng = dorm.Latitude, dorm.Longitude
			u.Address, u.City, u.Road, u.Poi = dorm.Address, dorm.City, dorm.Road, dorm.Poi
		}
	}
	// Legacy raw-coord setters (still accepted, e.g. for admin override).
	if req.Latitude != nil {
		u.Lat = *req.Latitude
	}
	if req.Longitude != nil {
		u.Lng = *req.Longitude
	}
	if req.Address != nil {
		u.Address = *req.Address
	}
	if req.City != nil {
		u.City = *req.City
	}
	if req.Road != nil {
		u.Road = *req.Road
	}
	if req.Poi != nil {
		u.Poi = *req.Poi
	}
	if req.DeviceModel != nil {
		u.DeviceModel = *req.DeviceModel
	}
	if req.DeviceSystem != nil {
		u.DeviceSystem = *req.DeviceSystem
	}
	if req.NotifyEmail != nil {
		u.NotifyEmail = strings.TrimSpace(*req.NotifyEmail)
	}
	if req.NotifyEnabled != nil {
		u.NotifyEnabled = *req.NotifyEnabled
	}
	if req.ServerChanKey != nil {
		u.ServerChanKey = strings.TrimSpace(*req.ServerChanKey)
	}
	if req.ServerChanEnabled != nil {
		u.ServerChanEnabled = *req.ServerChanEnabled
	}
	if req.SignDays != nil {
		if *req.SignDays < 0 || *req.SignDays > 127 {
			writeErr(w, http.StatusBadRequest, "signDays 必须在 0–127 之间（7-bit 周一到周日）")
			return
		}
		u.SignDays = *req.SignDays
	}
	if req.TriggerMinute != nil {
		if *req.TriggerMinute < 0 || *req.TriggerMinute > 29 {
			writeErr(w, http.StatusBadRequest, "triggerMinute 必须在 0–29 之间")
			return
		}
		u.TriggerMinute = *req.TriggerMinute
	}
	if req.JitterSec != nil {
		if *req.JitterSec < 0 || *req.JitterSec > 600 {
			writeErr(w, http.StatusBadRequest, "jitterSec 必须在 0–600 之间")
			return
		}
		u.JitterSec = *req.JitterSec
	}
	if req.RetryCount != nil {
		if *req.RetryCount < 0 || *req.RetryCount > 5 {
			writeErr(w, http.StatusBadRequest, "retryCount 必须在 0–5 之间")
			return
		}
		u.RetryCount = *req.RetryCount
	}
	if req.RetryGapMin != nil {
		if *req.RetryGapMin < 1 || *req.RetryGapMin > 15 {
			writeErr(w, http.StatusBadRequest, "retryGapMin 必须在 1–15 之间")
			return
		}
		u.RetryGapMin = *req.RetryGapMin
	}
	if req.SavedLocations != nil {
		// Validate it's a JSON array.
		var arr []store.SavedLocation
		if err := json.Unmarshal(*req.SavedLocations, &arr); err != nil {
			writeErr(w, http.StatusBadRequest, "savedLocations 不是合法 JSON 数组")
			return
		}
		b, _ := json.Marshal(arr)
		u.SavedLocations = string(b)
	}
	if err := h.store.UpdateSettings(r.Context(), u); err != nil {
		writeErr(w, http.StatusInternalServerError, "保存失败")
		return
	}
	writeJSON(w, http.StatusOK, settingsDTO(u))
}

// ---------- GET /api/v1/records ----------

func (h *handlers) records(w http.ResponseWriter, r *http.Request) {
	list, err := h.store.ListRecords(r.Context(), userIDOf(r), 100)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, rec := range list {
		out = append(out, map[string]any{
			"id":         rec.ID,
			"ruleId":     rec.RuleID,
			"status":     rec.Status,
			"message":    rec.Message,
			"occurredAt": rec.OccurredAt.Unix(),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------- POST /api/v1/sign-now ----------

func (h *handlers) signNow(w http.ResponseWriter, r *http.Request) {
	uid := userIDOf(r)
	u, err := h.store.GetUser(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusNotFound, "用户不存在")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	res := h.sched.SignOnce(ctx, u)
	_ = h.store.AddRecord(ctx, &store.Record{
		UserID: uid, RuleID: res.RuleID,
		Status: res.Status, Message: res.Message,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  res.Status,
		"message": res.Message,
	})
}

// ---------- PUT /api/v1/pin ----------

type changePinReq struct {
	OldPin string `json:"oldPin"`
	NewPin string `json:"newPin"`
}

func (h *handlers) changePin(w http.ResponseWriter, r *http.Request) {
	uid := userIDOf(r)
	var req changePinReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if !validPin(req.NewPin) {
		writeErr(w, http.StatusBadRequest, "新 PIN 必须是 4–6 位数字")
		return
	}
	u, err := h.store.GetUser(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusNotFound, "用户不存在")
		return
	}
	if len(u.PinHash) > 0 {
		if err := bcrypt.CompareHashAndPassword(u.PinHash, []byte(strings.TrimSpace(req.OldPin))); err != nil {
			writeErr(w, http.StatusUnauthorized, "旧 PIN 不正确")
			return
		}
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(strings.TrimSpace(req.NewPin)), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "PIN 哈希失败")
		return
	}
	if err := h.store.SetPinHash(r.Context(), uid, newHash); err != nil {
		writeErr(w, http.StatusInternalServerError, "保存失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- POST /api/v1/logout, DELETE /api/v1/me ----------

func (h *handlers) logout(w http.ResponseWriter, r *http.Request) {
	if ck, err := r.Cookie(sessionCookie); err == nil {
		_ = h.store.DeleteSession(r.Context(), ck.Value)
	}
	clearCookie(w, sessionCookie)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/v1/notify/test-serverchan — push a test notification using the
// user's currently saved Server酱 SendKey (NOT the value in any pending form;
// the user must save first). Returns ok or a 502 with the upstream error.
func (h *handlers) testServerChan(w http.ResponseWriter, r *http.Request) {
	u, err := h.store.GetUser(r.Context(), userIDOf(r))
	if err != nil {
		writeErr(w, http.StatusNotFound, "用户不存在")
		return
	}
	if u.ServerChanKey == "" {
		writeErr(w, http.StatusBadRequest, "尚未保存 Server酱 SendKey")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	client := notify.NewServerChan(u.ServerChanKey)
	if err := client.Send(ctx,
		"[wangui] Server酱 测试推送",
		fmt.Sprintf("如果你收到了这条消息，说明 Server酱 配置已生效。\n\n**姓名**：%s\n\n**学号**：`%s`\n\n**时间**：%s",
			u.UserName, u.UserNumber, time.Now().Format("2006-01-02 15:04:05")),
	); err != nil {
		writeErr(w, http.StatusBadGateway, "推送失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// deleteMe wipes user data AND unbinds the invite code (so the user can re-activate).
// Actually per design, invite codes are permanent — but if the user explicitly deletes
// their account, freeing the code lets them re-bind to a fresh user_id. Acceptable.
func (h *handlers) deleteMe(w http.ResponseWriter, r *http.Request) {
	uid := userIDOf(r)
	if err := h.store.DeleteUserAndUnbindCode(r.Context(), uid); err != nil {
		writeErr(w, http.StatusInternalServerError, "注销失败")
		return
	}
	clearCookie(w, sessionCookie)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
