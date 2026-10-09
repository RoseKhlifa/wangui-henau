package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/csv"
	"encoding/json"
	"errors"
	mathrand "math/rand/v2"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	apiclient "wangui/internal/api"
	"wangui/internal/events"
	"wangui/internal/notify"
	"wangui/internal/store"
)

const adminUserID = "__admin__"

// POST /api/v1/admin/login
func (h *handlers) adminLogin(w http.ResponseWriter, r *http.Request) {
	if h.adminPass == "" {
		writeErr(w, http.StatusServiceUnavailable, "管理员功能未启用 (未设置 WANGUI_ADMIN_PASS)")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Password), []byte(h.adminPass)) != 1 {
		writeErr(w, http.StatusUnauthorized, "密码错误")
		return
	}
	sess, err := h.store.CreateSession(r.Context(), adminUserID, true, 7*24*time.Hour)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "会话创建失败")
		return
	}
	setSessionCookie(w, adminSessionCookie, sess.SessionID, sess.ExpiresAt)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/v1/admin/logout
func (h *handlers) adminLogout(w http.ResponseWriter, r *http.Request) {
	if ck, err := r.Cookie(adminSessionCookie); err == nil {
		_ = h.store.DeleteSession(r.Context(), ck.Value)
	}
	clearCookie(w, adminSessionCookie)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// GET /api/v1/admin/me  — used by frontend to check admin login state
func (h *handlers) adminMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"isAdmin": true})
}

// GET /api/v1/admin/stats
func (h *handlers) adminStats(w http.ResponseWriter, r *http.Request) {
	totalUsers, _ := h.store.CountUsers(r.Context())
	totalGuests, _ := h.store.CountGuests(r.Context())
	totalCodes, usedCodes, _ := h.store.CountCodes(r.Context())
	today, _ := h.store.CountTodayRecords(r.Context())

	// Token expiry warnings: count regular users whose token expires within
	// 24h. Guests are excluded — they get auto-cleaned, no need to alert.
	users, _ := h.store.ListUsers(r.Context(), store.UserListFilter{Limit: 500})
	expiring := 0
	disabled := 0
	for _, u := range users {
		if u.IsGuest {
			continue
		}
		if time.Until(u.TokenExp) < 24*time.Hour && !u.IsDisabled {
			expiring++
		}
		if u.IsDisabled {
			disabled++
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"users": map[string]any{
			"total":    totalUsers - totalGuests,
			"guests":   totalGuests,
			"disabled": disabled,
			"expiring": expiring,
		},
		"codes": map[string]any{
			"total":  totalCodes,
			"used":   usedCodes,
			"unused": totalCodes - usedCodes,
		},
		"today": today,
	})
}

// GET /api/v1/admin/codes
func (h *handlers) adminListCodes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	codes, err := h.store.ListAdminCodes(r.Context(), store.CodeFilter{
		Status: q.Get("status"),
		Search: q.Get("search"),
		Limit:  atoiDefault(q.Get("limit"), 100),
		Offset: atoiDefault(q.Get("offset"), 0),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	out := make([]map[string]any, 0, len(codes))
	for _, c := range codes {
		out = append(out, codeDTO(c))
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /api/v1/admin/codes
func (h *handlers) adminCreateCodes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Count int    `json:"count"`
		Note  string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if req.Count <= 0 {
		req.Count = 1
	}
	codes, err := h.store.CreateCodes(r.Context(), req.Count, req.Note, adminIDOf(r))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(codes))
	for _, c := range codes {
		out = append(out, codeDTO(c))
	}
	writeJSON(w, http.StatusOK, out)
}

// PUT /api/v1/admin/codes/{code}
func (h *handlers) adminUpdateCode(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	var req struct {
		Note     *string `json:"note"`
		Disabled *bool   `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := h.store.UpdateCode(r.Context(), code, req.Note, req.Disabled); err != nil {
		writeErr(w, http.StatusInternalServerError, "保存失败")
		return
	}
	c, err := h.store.GetCode(r.Context(), code)
	if err != nil {
		writeErr(w, http.StatusNotFound, "邀请码不存在")
		return
	}
	writeJSON(w, http.StatusOK, codeDTO(c))
}

// DELETE /api/v1/admin/codes/{code}
func (h *handlers) adminDeleteCode(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	c, err := h.store.GetCode(r.Context(), code)
	if err != nil {
		writeErr(w, http.StatusNotFound, "邀请码不存在")
		return
	}
	if c.Used() {
		writeErr(w, http.StatusForbidden, "已绑定的邀请码不能删除，可改为禁用")
		return
	}
	if err := h.store.DeleteCode(r.Context(), code); err != nil {
		writeErr(w, http.StatusInternalServerError, "删除失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// GET /api/v1/admin/users
func (h *handlers) adminListUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	users, err := h.store.ListUsers(r.Context(), store.UserListFilter{
		Search: q.Get("search"),
		Limit:  atoiDefault(q.Get("limit"), 100),
		Offset: atoiDefault(q.Get("offset"), 0),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	// Pre-fetch dorm names once to enrich the user rows without N+1 queries.
	dormNames := map[int64]string{}
	if dorms, err := h.store.ListDorms(r.Context()); err == nil {
		for _, d := range dorms {
			dormNames[d.ID] = d.Name
		}
	}
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		if u.IsGuest {
			// Guests have their own admin page; don't double-list them here.
			continue
		}
		dto := adminUserDTO(u)
		if u.DormID != nil {
			dto["dormId"] = *u.DormID
			if name, ok := dormNames[*u.DormID]; ok {
				dto["dormName"] = name
			}
		}
		// Include last 3 records so the cards on the users page can show a
		// quick history without an N+1 round trip from the frontend.
		if recs, err := h.store.ListRecords(r.Context(), u.UserID, 3); err == nil {
			rec := make([]map[string]any, 0, len(recs))
			for _, x := range recs {
				rec = append(rec, map[string]any{
					"id":         x.ID,
					"status":     x.Status,
					"message":    x.Message,
					"occurredAt": x.OccurredAt.Unix(),
				})
			}
			dto["recentRecords"] = rec
		}
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, out)
}

// GET /api/v1/admin/dorms/{id}/users — list users bound to a dorm.
func (h *handlers) adminDormUsers(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 无效")
		return
	}
	users, err := h.store.ListUsersByDorm(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, map[string]any{
			"userId":      u.UserID,
			"userName":    u.UserName,
			"userNumber":  u.UserNumber,
			"userSection": u.UserSection,
			"userClass":   u.UserClass,
			"isDisabled":  u.IsDisabled,
			"autoSign":    u.AutoSign,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// GET /api/v1/admin/users/{id}
func (h *handlers) adminGetUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	u, err := h.store.GetUser(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "用户不存在")
			return
		}
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	recs, _ := h.store.ListRecords(r.Context(), id, 50)
	recDTOs := make([]map[string]any, 0, len(recs))
	for _, rec := range recs {
		recDTOs = append(recDTOs, map[string]any{
			"id":         rec.ID,
			"status":     rec.Status,
			"message":    rec.Message,
			"occurredAt": rec.OccurredAt.Unix(),
		})
	}
	dto := adminUserDTO(u)
	if u.DormID != nil {
		dto["dormId"] = *u.DormID
		if d, err := h.store.GetDorm(r.Context(), *u.DormID); err == nil {
			dto["dormName"] = d.Name
		}
	}
	dto["recentRecords"] = recDTOs
	writeJSON(w, http.StatusOK, dto)
}

// PUT /api/v1/admin/users/{id}
func (h *handlers) adminUpdateUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		IsDisabled *bool  `json:"isDisabled"`
		AutoSign   *bool  `json:"autoSign"`
		DormID     *int64 `json:"dormId"`
		SignDays   *int   `json:"signDays"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	u, err := h.store.GetUser(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "用户不存在")
		return
	}
	if req.IsDisabled != nil {
		if err := h.store.SetDisabled(r.Context(), id, *req.IsDisabled); err != nil {
			writeErr(w, http.StatusInternalServerError, "保存失败")
			return
		}
		if *req.IsDisabled {
			_ = h.store.DeleteSessionsForUser(r.Context(), id)
		}
		u.IsDisabled = *req.IsDisabled
	}
	if req.AutoSign != nil {
		u.AutoSign = *req.AutoSign
		if err := h.store.UpdateSettings(r.Context(), u); err != nil {
			writeErr(w, http.StatusInternalServerError, "保存失败")
			return
		}
	}
	if req.SignDays != nil {
		if *req.SignDays < 0 || *req.SignDays > 127 {
			writeErr(w, http.StatusBadRequest, "signDays 必须在 0–127 之间")
			return
		}
		u.SignDays = *req.SignDays
		if err := h.store.UpdateSettings(r.Context(), u); err != nil {
			writeErr(w, http.StatusInternalServerError, "保存失败")
			return
		}
	}
	if req.DormID != nil {
		if *req.DormID == 0 {
			if err := h.store.SetUserDorm(r.Context(), id, nil); err != nil {
				writeErr(w, http.StatusInternalServerError, "保存失败")
				return
			}
			u.DormID = nil
		} else {
			dorm, err := h.store.GetDorm(r.Context(), *req.DormID)
			if err != nil {
				writeErr(w, http.StatusBadRequest, "宿舍楼不存在")
				return
			}
			if err := h.store.SetUserDorm(r.Context(), id, dorm); err != nil {
				writeErr(w, http.StatusInternalServerError, "保存失败")
				return
			}
			u.DormID = &dorm.ID
		}
	}
	writeJSON(w, http.StatusOK, adminUserDTO(u))
}

// POST /api/v1/rosekhlifa/users/{id}/token — admin refreshes a user's school
// token via wechat OAuth callback. Critical for guests, who have no PIN and
// can't log in to refresh their own token after the ~7-day school JWT expires.
// The new token's user_id (iss) MUST match {id} so admin can't accidentally
// overwrite the wrong account when a guest scans the wrong QR or pastes a
// callback meant for a different user.
func (h *handlers) adminRefreshUserToken(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cur, err := h.store.GetUser(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "用户不存在")
		return
	}
	var req schoolAuthInput
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	auth, status, err := h.resolveSchoolAuth(ctx, req)
	if err != nil {
		writeErr(w, status, err.Error())
		return
	}
	if auth.Claims.Iss != id {
		writeErr(w, http.StatusBadRequest,
			"Token 属于另一个账号 ("+auth.User.UserNumber+")，不能覆盖 "+cur.UserNumber)
		return
	}
	if err := h.store.UpdateToken(ctx, id, auth.Token, auth.Claims.ExpiresAt()); err != nil {
		writeErr(w, http.StatusInternalServerError, "保存失败")
		return
	}
	// Refresh display fields (name/avatar may have changed school-side) but
	// don't fail the request if this part errors.
	if auth.ProfileHydrated {
		if err := h.store.UpdateUserProfile(ctx, id,
			auth.User.UserName, auth.User.UserNumber,
			auth.User.UserSection, auth.User.UserClass,
			auth.User.UserAvatarURL); err != nil {
			h.log.Warn("refresh profile failed (token still updated)", "user", id, "err", err.Error())
		}
	}
	h.log.Info("admin refresh token", "target_user", id, "new_exp", auth.Claims.ExpiresAt())
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"expiresAt": auth.Claims.Exp,
	})
}

// GET /api/v1/rosekhlifa/users/{id}/checkin-status — fetch the school's
// view of "what should this user do tonight" without performing a sign.
// This is how admin sees that a user has filed leave (请假) or that today
// is a 节假日离校 day — info the school surfaces but our records don't.
//
// Cached client-side via the regular HTTP cache headers; this endpoint
// hits the school live each call. Rate-limited by school's own backend.
func (h *handlers) adminCheckinStatusForUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	u, err := h.store.GetUser(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "用户不存在")
		return
	}
	if time.Now().After(u.TokenExp) {
		writeJSON(w, http.StatusOK, map[string]any{
			"state":   "tokenExpired",
			"message": "Token 已过期，无法查询",
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	c := apiclient.New(u.Token)
	rule, err := c.CurrentRule(ctx)
	if err != nil {
		if apiclient.IsAuthExpired(err) {
			writeJSON(w, http.StatusOK, map[string]any{
				"state": "tokenExpired", "message": "Token 已失效，请刷新",
			})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"state": "error", "message": "签到规则获取失败: " + err.Error(),
		})
		return
	}
	st, err := c.CheckinStatus(ctx, rule.RuleID)
	if err != nil {
		if apiclient.IsAuthExpired(err) {
			writeJSON(w, http.StatusOK, map[string]any{
				"state":   "tokenExpired",
				"message": "Token 已失效，请刷新",
			})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"state":   "error",
			"message": "学校接口异常: " + err.Error(),
		})
		return
	}
	// Derive a single coarse state for UI rendering. Order matters — exempt
	// dominates "has checked in" since the school's own response sometimes
	// returns both.
	state := "pending"
	switch {
	case st.IsBoarding:
		state = "boarding"
	case st.IsExempt != nil && *st.IsExempt:
		state = "exempt"
	case st.HasCheckedIn != nil && *st.HasCheckedIn:
		state = "signed"
	case st.CanCheckin:
		state = "canSign"
	}
	out := map[string]any{
		"state":        state,
		"message":      st.Message,
		"canCheckin":   st.CanCheckin,
		"hasCheckedIn": st.HasCheckedIn,
		"isExempt":     st.IsExempt,
		"isBoarding":   st.IsBoarding,
		"exemptReason": st.ExemptReason,
	}
	if st.CurrentRule != nil {
		out["currentRule"] = map[string]any{
			"ruleId":      st.CurrentRule.RuleID,
			"ruleName":    st.CurrentRule.RuleName,
			"startTime":   st.CurrentRule.StartTime,
			"endTime":     st.CurrentRule.EndTime,
			"description": st.CurrentRule.Description,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /api/v1/rosekhlifa/users/{id}/sign-now — admin signs on behalf of any
// user (regular or guest). Same logic as the user-side /sign-now: invoke the
// scheduler once, persist a record. No email dispatch — admin already knows
// they hit the button.
func (h *handlers) adminSignNowForUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	u, err := h.store.GetUser(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "用户不存在")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	res := h.sched.SignOnce(ctx, u)
	_ = h.store.AddRecord(ctx, &store.Record{
		UserID: id, RuleID: res.RuleID,
		Status: res.Status, Message: res.Message,
	})
	h.log.Info("admin sign-now", "target_user", id, "status", res.Status)
	if h.bus != nil {
		h.bus.PublishJSON(events.TypeSignResult, map[string]any{
			"userId":         id,
			"userName":       u.UserName,
			"status":         res.Status,
			"message":        res.Message,
			"attempt":        0,
			"terminal":       true,
			"adminTriggered": true,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  res.Status,
		"message": res.Message,
	})
}

// DELETE /api/v1/admin/users/{id}
func (h *handlers) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.store.DeleteUserAndUnbindCode(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, "删除失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- Admin dorm management ----------

type adminDormReq struct {
	Name              string  `json:"name"`
	Latitude          float64 `json:"latitude"`
	Longitude         float64 `json:"longitude"`
	Address           string  `json:"address"`
	City              string  `json:"city"`
	Road              string  `json:"road"`
	Poi               string  `json:"poi"`
	Note              string  `json:"note"`
	SendAddressFields bool    `json:"sendAddressFields"`
}

func (h *handlers) adminListDorms(w http.ResponseWriter, r *http.Request) {
	dorms, err := h.store.ListDorms(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	out := make([]map[string]any, 0, len(dorms))
	for _, d := range dorms {
		used, _ := h.store.CountDormUsers(r.Context(), d.ID)
		out = append(out, dormDTO(d, used))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handlers) adminCreateDorm(w http.ResponseWriter, r *http.Request) {
	var req adminDormReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "名称不能为空")
		return
	}
	if req.Latitude == 0 || req.Longitude == 0 {
		writeErr(w, http.StatusBadRequest, "坐标必填")
		return
	}
	if strings.TrimSpace(req.Address) == "" {
		writeErr(w, http.StatusBadRequest, "详细地址必填（新版签到协议要求地址参与签名）")
		return
	}
	d := &store.Dorm{
		Name:              strings.TrimSpace(req.Name),
		Latitude:          req.Latitude,
		Longitude:         req.Longitude,
		Address:           req.Address,
		City:              req.City,
		Road:              req.Road,
		Poi:               req.Poi,
		Note:              req.Note,
		SendAddressFields: true,
	}
	if err := h.store.CreateDorm(r.Context(), d); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeErr(w, http.StatusConflict, "名称已存在")
			return
		}
		writeErr(w, http.StatusInternalServerError, "保存失败")
		return
	}
	writeJSON(w, http.StatusOK, dormDTO(d, 0))
}

func (h *handlers) adminUpdateDorm(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 无效")
		return
	}
	var req struct {
		Name              *string  `json:"name"`
		Latitude          *float64 `json:"latitude"`
		Longitude         *float64 `json:"longitude"`
		Address           *string  `json:"address"`
		City              *string  `json:"city"`
		Road              *string  `json:"road"`
		Poi               *string  `json:"poi"`
		Note              *string  `json:"note"`
		SendAddressFields *bool    `json:"sendAddressFields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if req.Address != nil && strings.TrimSpace(*req.Address) == "" {
		writeErr(w, http.StatusBadRequest, "详细地址不能为空（新版签到协议要求地址参与签名）")
		return
	}
	sendAddressFields := true
	req.SendAddressFields = &sendAddressFields
	if err := h.store.UpdateDorm(r.Context(), id,
		req.Name, req.Latitude, req.Longitude,
		req.Address, req.City, req.Road, req.Poi, req.Note,
		req.SendAddressFields); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeErr(w, http.StatusConflict, "名称已存在")
			return
		}
		writeErr(w, http.StatusInternalServerError, "保存失败")
		return
	}
	d, err := h.store.GetDorm(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "宿舍楼不存在")
		return
	}
	used, _ := h.store.CountDormUsers(r.Context(), id)
	writeJSON(w, http.StatusOK, dormDTO(d, used))
}

func (h *handlers) adminDeleteDorm(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 无效")
		return
	}
	used, _ := h.store.CountDormUsers(r.Context(), id)
	if used > 0 {
		writeErr(w, http.StatusForbidden,
			"已有 "+strconv.Itoa(used)+" 个用户绑定此宿舍楼，不能删除")
		return
	}
	if err := h.store.DeleteDorm(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, "删除失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func dormDTO(d *store.Dorm, users int) map[string]any {
	return map[string]any{
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
		"users":             users,
		"createdAt":         d.CreatedAt.Unix(),
		"updatedAt":         d.UpdatedAt.Unix(),
	}
}

// ---------- Admin SMTP config ----------

// GET /api/v1/rosekhlifa/smtp — returns config; password is masked.
func (h *handlers) adminGetSMTP(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.store.GetSMTPConfig(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":                cfg.Enabled,
		"host":                   cfg.Host,
		"port":                   cfg.Port,
		"username":               cfg.Username,
		"from":                   cfg.From,
		"adminBcc":               cfg.AdminBcc,
		"passwordSet":            cfg.Password != "",
		"adminServerChanEnabled": cfg.AdminServerChanEnabled,
		"adminServerChanKeySet":  cfg.AdminServerChanKey != "",
	})
}

// PUT /api/v1/rosekhlifa/smtp — update notify config. Empty password keeps
// the existing one; same for adminServerChanKey ("" means keep current).
func (h *handlers) adminUpdateSMTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled                bool   `json:"enabled"`
		Host                   string `json:"host"`
		Port                   int    `json:"port"`
		Username               string `json:"username"`
		Password               string `json:"password"`
		From                   string `json:"from"`
		AdminBcc               string `json:"adminBcc"`
		AdminServerChanKey     string `json:"adminServerChanKey"`
		AdminServerChanEnabled bool   `json:"adminServerChanEnabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if req.Host == "" {
		req.Host = "smtp.gmail.com"
	}
	if req.Port == 0 {
		req.Port = 587
	}
	cfg := &store.SMTPConfig{
		Enabled:  req.Enabled,
		Host:     strings.TrimSpace(req.Host),
		Port:     req.Port,
		Username: strings.TrimSpace(req.Username),
		// Gmail app passwords are shown grouped as "glht egbx rokr ktiu"
		// (4×4 with spaces) but the real value has no whitespace at all.
		// strings.Fields splits on any whitespace, then re-joins with "".
		Password:               strings.Join(strings.Fields(req.Password), ""),
		From:                   strings.TrimSpace(req.From),
		AdminBcc:               strings.TrimSpace(req.AdminBcc),
		AdminServerChanKey:     strings.TrimSpace(req.AdminServerChanKey),
		AdminServerChanEnabled: req.AdminServerChanEnabled,
	}
	if err := h.store.SetSMTPConfig(r.Context(), cfg); err != nil {
		writeErr(w, http.StatusInternalServerError, "保存失败")
		return
	}
	saved, _ := h.store.GetSMTPConfig(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                     true,
		"enabled":                saved.Enabled,
		"host":                   saved.Host,
		"port":                   saved.Port,
		"username":               saved.Username,
		"from":                   saved.From,
		"adminBcc":               saved.AdminBcc,
		"passwordSet":            saved.Password != "",
		"adminServerChanEnabled": saved.AdminServerChanEnabled,
		"adminServerChanKeySet":  saved.AdminServerChanKey != "",
	})
}

// POST /api/v1/rosekhlifa/serverchan/test — push a test notification to the
// admin's saved Server酱 SendKey.
func (h *handlers) adminTestServerChan(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.store.GetSMTPConfig(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	if cfg.AdminServerChanKey == "" {
		writeErr(w, http.StatusBadRequest, "Admin Server酱 SendKey 未配置")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	client := notify.NewServerChan(cfg.AdminServerChanKey)
	if err := client.Send(ctx,
		"[勿外传] Server酱 测试推送",
		"如果你的微信收到了这条消息，说明 admin Server酱 配置已生效。\n\n时间："+time.Now().Format("2006-01-02 15:04:05"),
	); err != nil {
		writeErr(w, http.StatusBadGateway, "推送失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/v1/rosekhlifa/smtp/test — send a real test email to adminBcc
// (or username if no bcc). Uses the *currently-saved* config, not the form.
func (h *handlers) adminTestSMTP(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.store.GetSMTPConfig(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	if cfg.Host == "" || cfg.Username == "" || cfg.Password == "" {
		writeErr(w, http.StatusBadRequest, "SMTP 未完整配置（缺 host/username/password）")
		return
	}
	target := cfg.AdminBcc
	if target == "" {
		target = cfg.Username
	}
	client := &notify.EmailClient{
		Host: cfg.Host, Port: cfg.Port,
		Username: cfg.Username, Password: cfg.Password, From: cfg.From,
	}
	if err := client.Send(notify.Message{
		To:      target,
		Subject: "[勿外传] SMTP 测试邮件",
		Text:    "如果你收到了这封邮件，说明 SMTP 配置已生效。\n\n时间：" + time.Now().Format("2006-01-02 15:04:05"),
		HTML: `<!doctype html><html><body style="font-family:-apple-system,Segoe UI,sans-serif;background:#fafafa;padding:24px;color:#18181b;">
<div style="max-width:520px;margin:0 auto;background:#fff;border:1px solid #e5e7eb;border-radius:12px;padding:20px;">
<div style="font-size:11px;color:#71717a;letter-spacing:.1em;text-transform:uppercase;">勿外传 · SMTP 测试</div>
<h2 style="font-size:18px;margin:6px 0 12px 0;">✅ 配置已生效</h2>
<p style="font-size:14px;color:#3f3f46;line-height:1.6;">如果你收到了这封邮件，说明 SMTP 设置正确，签到通知将正常发出。</p>
<p style="font-size:12px;color:#a1a1aa;margin-top:18px;">时间 ` + time.Now().Format("2006-01-02 15:04:05") + `</p>
</div></body></html>`,
	}); err != nil {
		writeErr(w, http.StatusBadGateway, "发送失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sentTo": target})
}

// GET /api/v1/rosekhlifa/records.csv?from=YYYY-MM-DD&to=YYYY-MM-DD
//
// Streams a CSV of every sign record in the date range. Used by admin to
// archive logs (e.g. if the school ever asks for evidence of activity).
// Defaults: from = first day of current month, to = today.
//
// Columns:
//
//	id, occurred_at_iso, user_id, user_name, user_number, status, message, rule_id
//
// occurred_at is ISO 8601 in the server's local timezone (CST) for Excel
// friendliness — raw unix timestamps confuse non-technical users.
func (h *handlers) adminExportRecordsCSV(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	loc := time.Now().Location()
	now := time.Now().In(loc)
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	to := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, loc)
	if v := q.Get("from"); v != "" {
		if t, err := time.ParseInLocation("2006-01-02", v, loc); err == nil {
			from = t
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := time.ParseInLocation("2006-01-02", v, loc); err == nil {
			to = time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, loc)
		}
	}
	if to.Before(from) {
		writeErr(w, http.StatusBadRequest, "to 必须不早于 from")
		return
	}

	recs, err := h.store.ListAllRecordsBetween(r.Context(), from, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}

	// Build a user_id → (number, name) index so we can include 学号 in the
	// CSV without an N+1 lookup per row.
	users, _ := h.store.ListUsers(r.Context(), store.UserListFilter{Limit: 500})
	numByID := map[string]string{}
	nameByID := map[string]string{}
	for _, u := range users {
		numByID[u.UserID] = u.UserNumber
		nameByID[u.UserID] = u.UserName
	}

	filename := "wangui-records-" +
		from.Format("20060102") + "-" + to.Format("20060102") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	// UTF-8 BOM so Excel auto-detects the encoding and shows 中文 correctly.
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})

	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write([]string{"id", "occurred_at", "user_id", "user_name", "user_number", "status", "message", "rule_id"})
	for _, rec := range recs {
		name := rec.UserName
		if name == "" {
			name = nameByID[rec.UserID]
		}
		_ = cw.Write([]string{
			strconv.FormatInt(rec.ID, 10),
			rec.OccurredAt.In(loc).Format("2006-01-02 15:04:05"),
			rec.UserID,
			name,
			numByID[rec.UserID],
			rec.Status,
			rec.Message,
			strconv.Itoa(rec.RuleID),
		})
	}
}

// GET /api/v1/rosekhlifa/school-rules — return the most recent cached rules
// snapshot taken by the daily rules-watch sweep, plus when it was taken.
// If never sampled (fresh DB), returns null + 0.
func (h *handlers) adminSchoolRules(w http.ResponseWriter, r *http.Request) {
	snap, err := h.store.GetConfig(r.Context(), "schoolrules.snapshot")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	tsStr, _ := h.store.GetConfig(r.Context(), "schoolrules.updated_at")
	updatedAt, _ := strconv.ParseInt(tsStr, 10, 64)
	var rules any
	if snap != "" {
		_ = json.Unmarshal([]byte(snap), &rules)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rules":     rules,
		"updatedAt": updatedAt,
	})
}

// GET /api/v1/admin/logs — recent sign records across all users
func (h *handlers) adminLogs(w http.ResponseWriter, r *http.Request) {
	limit := atoiDefault(r.URL.Query().Get("limit"), 100)
	recs, err := h.store.ListAllRecords(r.Context(), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	out := make([]map[string]any, 0, len(recs))
	for _, rec := range recs {
		out = append(out, map[string]any{
			"id":         rec.ID,
			"userId":     rec.UserID,
			"userName":   rec.UserName,
			"status":     rec.Status,
			"message":    rec.Message,
			"occurredAt": rec.OccurredAt.Unix(),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------- DTOs ----------

func codeDTO(c *store.InviteCode) map[string]any {
	var bound *string
	var boundAt *int64
	if c.BoundUserID != nil {
		bound = c.BoundUserID
	}
	if c.BoundAt != nil {
		t := c.BoundAt.Unix()
		boundAt = &t
	}
	return map[string]any{
		"code":          c.Code,
		"boundUserId":   bound,
		"boundUserName": c.BoundUserName,
		"boundAt":       boundAt,
		"note":          c.Note,
		"disabled":      c.Disabled,
		"createdAt":     c.CreatedAt.Unix(),
		"createdBy":     c.CreatedBy,
		"used":          c.Used(),
	}
}

// POST /api/v1/admin/users/{id}/pin — reset a user's login PIN.
// If newPin is provided, validate (4–6 digits) and use it.
// If newPin is empty, server generates a random 6-digit PIN.
// Always logs out the user's existing sessions.
func (h *handlers) adminResetUserPin(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		NewPin string `json:"newPin"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	pin := strings.TrimSpace(req.NewPin)
	if pin == "" {
		pin = generateNumericPin(6)
	} else if !validPin(pin) {
		writeErr(w, http.StatusBadRequest, "PIN 必须是 4–6 位数字")
		return
	}

	if _, err := h.store.GetUser(r.Context(), id); err != nil {
		writeErr(w, http.StatusNotFound, "用户不存在")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "PIN 哈希失败")
		return
	}
	if err := h.store.SetPinHash(r.Context(), id, hash); err != nil {
		writeErr(w, http.StatusInternalServerError, "保存失败")
		return
	}
	// Force re-login.
	_ = h.store.DeleteSessionsForUser(r.Context(), id)
	h.log.Info("admin reset pin", "target_user", id)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"newPin": pin,
	})
}

func generateNumericPin(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand should not fail; fall back to deterministic
		for i := range buf {
			buf[i] = '0'
		}
		return string(buf)
	}
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = '0' + b%10
	}
	return string(out)
}

func adminUserDTO(u *store.User) map[string]any {
	return map[string]any{
		"userId":        u.UserID,
		"userName":      u.UserName,
		"userNumber":    u.UserNumber,
		"userSection":   u.UserSection,
		"userClass":     u.UserClass,
		"userAvatarUrl": u.UserAvatarURL,
		"inviteCode":    u.InviteCode,
		"isDisabled":    u.IsDisabled,
		"autoSign":      u.AutoSign,
		"latitude":      u.Lat,
		"longitude":     u.Lng,
		"tokenExp":      u.TokenExp.Unix(),
		"tokenValid":    time.Now().Before(u.TokenExp),
		"createdAt":     u.CreatedAt.Unix(),
		"updatedAt":     u.UpdatedAt.Unix(),
		"signDays":      u.SignDays,
		"triggerMinute": u.TriggerMinute,
		"jitterSec":     u.JitterSec,
	}
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// ============================================================================
// Guest endpoints: admin-managed temporary users that sign on specific
// calendar dates, no PIN, auto-cleanup after the last date.
// ============================================================================

type guestCreateReq struct {
	schoolAuthInput
	Label     string   `json:"label"`
	SignDates []string `json:"signDates"` // ["2026-05-20", ...]
	DormID    int64    `json:"dormId"`    // optional, 0 = no dorm bound
}

type guestUpdateReq struct {
	Label     *string  `json:"label"`
	SignDates []string `json:"signDates"`
}

// GET /api/v1/rosekhlifa/guests
func (h *handlers) adminListGuests(w http.ResponseWriter, r *http.Request) {
	guests, err := h.store.ListGuests(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	out := make([]map[string]any, 0, len(guests))
	for _, g := range guests {
		out = append(out, h.guestDTO(r.Context(), g))
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /api/v1/rosekhlifa/guests
//
// Body: {label, signDates: [...], dormId?, callbackUrl|oauthCode|token}
//
// Resolves the school OAuth code → JWT → user identity, writes a guest user
// record. ExpiresAt is auto-set to (max(signDates) + 1 day) so the cleanup
// ticker picks it up after the last sign date passes.
func (h *handlers) adminCreateGuest(w http.ResponseWriter, r *http.Request) {
	var req guestCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		writeErr(w, http.StatusBadRequest, "备注名不能为空")
		return
	}
	dates := normalizeSignDates(req.SignDates)
	if len(dates) == 0 {
		writeErr(w, http.StatusBadRequest, "至少要选一个有效签到日期 (YYYY-MM-DD)")
		return
	}
	maxDate, err := time.ParseInLocation("2006-01-02", dates[len(dates)-1], time.Local)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "日期解析失败")
		return
	}
	// Cleanup runs at 02:00 each day; setting expires_at to next-day 00:00
	// means a guest signing on May 20 is gone by May 21 02:00.
	expiresAt := time.Date(maxDate.Year(), maxDate.Month(), maxDate.Day(), 0, 0, 0, 0, maxDate.Location()).AddDate(0, 0, 1)

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	auth, status, err := h.resolveSchoolAuth(ctx, req.schoolAuthInput)
	if err != nil {
		writeErr(w, status, err.Error())
		return
	}

	// Refuse to overwrite an existing non-guest account with the same user_id.
	var existingGuest *store.User
	if existing, err := h.store.GetUser(ctx, auth.Claims.Iss); err == nil {
		if !existing.IsGuest {
			writeErr(w, http.StatusConflict,
				"该学号 ("+auth.User.UserNumber+") 已存在正式账号，不能创建为临时朋友")
			return
		}
		// Existing guest: we'll overwrite (admin is re-issuing dates).
		existingGuest = existing
	}
	if !auth.ProfileHydrated && existingGuest != nil {
		auth.User.UserName = existingGuest.UserName
		auth.User.UserNumber = existingGuest.UserNumber
		auth.User.UserSection = existingGuest.UserSection
		auth.User.UserClass = existingGuest.UserClass
		auth.User.UserAvatarURL = existingGuest.UserAvatarURL
	}

	datesJSON, _ := json.Marshal(dates)

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
		DeviceModel:   "Pixel 7",
		DeviceSystem:  "Android 13",
		TriggerMinute: mathrand.IntN(10),
		JitterSec:     60,
		IsGuest:       true,
		GuestLabel:    label,
		SignDates:     string(datesJSON),
		ExpiresAt:     &expiresAt,
	}
	if err := h.store.UpsertUser(ctx, u); err != nil {
		h.log.Error("upsert guest", "err", err.Error())
		writeErr(w, http.StatusInternalServerError, "保存失败")
		return
	}
	// UpsertUser's ON CONFLICT branch only touches identity + token. Guest
	// fields on an existing record need a follow-up UPDATE.
	if err := h.store.UpdateGuestSchedule(ctx, u.UserID, label, string(datesJSON), &expiresAt); err != nil {
		h.log.Error("update guest schedule", "err", err.Error())
		writeErr(w, http.StatusInternalServerError, "保存失败")
		return
	}

	if req.DormID > 0 {
		if dorm, err := h.store.GetDorm(ctx, req.DormID); err == nil {
			_ = h.store.SetUserDorm(ctx, u.UserID, dorm)
		}
	}

	reloaded, err := h.store.GetUser(ctx, u.UserID)
	if err != nil {
		reloaded = u
	}
	h.log.Info("guest created", "user", u.UserID, "label", label, "dates", len(dates))
	writeJSON(w, http.StatusOK, h.guestDTO(ctx, reloaded))
}

// PUT /api/v1/rosekhlifa/guests/{id}
//
// Update label and/or sign_dates. When dates change, expires_at is recomputed.
func (h *handlers) adminUpdateGuest(w http.ResponseWriter, r *http.Request) {
	uid := chi.URLParam(r, "id")
	var req guestUpdateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	cur, err := h.store.GetUser(r.Context(), uid)
	if err != nil || !cur.IsGuest {
		writeErr(w, http.StatusNotFound, "临时朋友不存在")
		return
	}
	label := cur.GuestLabel
	if req.Label != nil {
		label = strings.TrimSpace(*req.Label)
	}
	signDates := cur.SignDates
	expiresAt := cur.ExpiresAt
	if req.SignDates != nil {
		dates := normalizeSignDates(req.SignDates)
		if len(dates) == 0 {
			writeErr(w, http.StatusBadRequest, "至少要选一个有效签到日期")
			return
		}
		maxDate, _ := time.ParseInLocation("2006-01-02", dates[len(dates)-1], time.Local)
		e := time.Date(maxDate.Year(), maxDate.Month(), maxDate.Day(), 0, 0, 0, 0, maxDate.Location()).AddDate(0, 0, 1)
		expiresAt = &e
		datesJSON, _ := json.Marshal(dates)
		signDates = string(datesJSON)
	}
	if err := h.store.UpdateGuestSchedule(r.Context(), uid, label, signDates, expiresAt); err != nil {
		writeErr(w, http.StatusInternalServerError, "保存失败")
		return
	}
	if updated, err := h.store.GetUser(r.Context(), uid); err == nil {
		writeJSON(w, http.StatusOK, h.guestDTO(r.Context(), updated))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// DELETE /api/v1/rosekhlifa/guests/{id}
func (h *handlers) adminDeleteGuest(w http.ResponseWriter, r *http.Request) {
	uid := chi.URLParam(r, "id")
	cur, err := h.store.GetUser(r.Context(), uid)
	if err != nil || !cur.IsGuest {
		writeErr(w, http.StatusNotFound, "临时朋友不存在")
		return
	}
	if err := h.store.DeleteUser(r.Context(), uid); err != nil {
		writeErr(w, http.StatusInternalServerError, "删除失败")
		return
	}
	h.log.Info("guest deleted", "user", uid, "label", cur.GuestLabel)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *handlers) guestDTO(ctx context.Context, u *store.User) map[string]any {
	var dates []string
	_ = json.Unmarshal([]byte(u.SignDates), &dates)
	out := map[string]any{
		"userId":        u.UserID,
		"userName":      u.UserName,
		"userNumber":    u.UserNumber,
		"userSection":   u.UserSection,
		"userClass":     u.UserClass,
		"userAvatarUrl": u.UserAvatarURL,
		"label":         u.GuestLabel,
		"signDates":     dates,
		"tokenExp":      u.TokenExp.Unix(),
		"tokenValid":    time.Now().Before(u.TokenExp),
		"createdAt":     u.CreatedAt.Unix(),
		"dormId":        u.DormID,
		"autoSign":      u.AutoSign,
		"isDisabled":    u.IsDisabled,
		"triggerMinute": u.TriggerMinute,
		"jitterSec":     u.JitterSec,
	}
	if u.ExpiresAt != nil {
		out["expiresAt"] = u.ExpiresAt.Unix()
	} else {
		out["expiresAt"] = nil
	}
	if u.DormID != nil {
		if d, err := h.store.GetDorm(ctx, *u.DormID); err == nil {
			out["dormName"] = d.Name
		}
	}
	// Recent records so the admin can see the guest's last few outcomes
	// without leaving the card view.
	if recs, err := h.store.ListRecords(ctx, u.UserID, 5); err == nil {
		rec := make([]map[string]any, 0, len(recs))
		for _, r := range recs {
			rec = append(rec, map[string]any{
				"id":         r.ID,
				"status":     r.Status,
				"message":    r.Message,
				"occurredAt": r.OccurredAt.Unix(),
			})
		}
		out["recentRecords"] = rec
	}
	return out
}

// normalizeSignDates parses, validates, dedupes, and sorts a list of
// "YYYY-MM-DD" strings ascending. Invalid entries silently dropped; empty
// result means no valid dates remained.
func normalizeSignDates(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range in {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		t, err := time.ParseInLocation("2006-01-02", s, time.Local)
		if err != nil {
			continue
		}
		norm := t.Format("2006-01-02")
		if seen[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, norm)
	}
	sort.Strings(out)
	return out
}
