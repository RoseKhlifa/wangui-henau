package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	apiclient "wangui/internal/api"
	"wangui/internal/scheduler"
)

// signDebugReq is the body for POST /rosekhlifa/users/{id}/sign-debug.
// Every override is optional; missing fields fall back to the user's
// current saved values so admin can iterate field-by-field.
type signDebugReq struct {
	// Coordinate overrides. Useful for testing what coords school will accept.
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`

	// Speculative new fields the school may now require.
	Accuracy  *float64 `json:"accuracy"`
	Altitude  *float64 `json:"altitude"`
	Speed     *float64 `json:"speed"`
	CoordType *string  `json:"coordType"`
	Timestamp *int64   `json:"timestamp"`

	// Address-block overrides.
	LocationAddress *string `json:"locationAddress"`
	City            *string `json:"city"`
	Road            *string `json:"road"`
	Poi             *string `json:"poi"`

	// Device fingerprint overrides.
	DeviceModel  *string `json:"deviceModel"`
	DeviceSystem *string `json:"deviceSystem"`

	// RuleID override (defaults to 1).
	RuleID *int `json:"ruleId"`

	// If DryRun is true, we do NOT actually call the school. We only build
	// the request body and return it. Handy for inspecting what gets sent
	// before risking a real attempt.
	DryRun bool `json:"dryRun"`
}

// POST /api/v1/rosekhlifa/users/{id}/sign-debug
//
// Iteratively probe the school's /checkin endpoint with arbitrary payload
// tweaks. Returns the full request body sent, the HTTP status, the parsed
// envelope, and the raw response body — letting admin discover which field
// the new geofence implementation actually rejects on, without having to
// open mobile DevTools.
//
// Unlike the regular /sign-now path, this does NOT persist a sign_records
// row or fire notifications: it's pure diagnostic.
func (h *handlers) adminSignDebug(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	u, err := h.store.GetUser(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "用户不存在")
		return
	}
	if u.Token == "" || time.Now().After(u.TokenExp) {
		writeErr(w, http.StatusBadRequest, "用户 token 无效或已过期，先刷新 Token")
		return
	}
	var req signDebugReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		writeErr(w, http.StatusBadRequest, "请求格式错误: "+err.Error())
		return
	}

	// Build the SignRequest by starting from what scheduler.SignOnce would send,
	// then applying every non-nil override from the debug request.
	ruleID := scheduler.DefaultRuleID
	if req.RuleID != nil {
		ruleID = *req.RuleID
	}
	body := apiclient.SignRequest{
		RuleID:       ruleID,
		Latitude:     u.Lat,
		Longitude:    u.Lng,
		DeviceModel:  nonEmpty(u.DeviceModel, "iPhone"),
		DeviceSystem: nonEmpty(u.DeviceSystem, "iOS"),
	}
	// If the user's dorm wants address fields, fill them; the debug call
	// can still override below.
	if u.SendAddressFields {
		body.LocationAddress = u.Address
		body.City = u.City
		body.Road = u.Road
		body.Poi = u.Poi
	}

	if req.Latitude != nil {
		body.Latitude = *req.Latitude
	}
	if req.Longitude != nil {
		body.Longitude = *req.Longitude
	}
	if req.Accuracy != nil {
		body.Accuracy = *req.Accuracy
	}
	if req.Altitude != nil {
		body.Altitude = *req.Altitude
	}
	if req.Speed != nil {
		body.Speed = *req.Speed
	}
	if req.CoordType != nil {
		body.CoordType = *req.CoordType
	}
	if req.Timestamp != nil {
		body.Timestamp = *req.Timestamp
	}
	if req.LocationAddress != nil {
		body.LocationAddress = *req.LocationAddress
	}
	if req.City != nil {
		body.City = *req.City
	}
	if req.Road != nil {
		body.Road = *req.Road
	}
	if req.Poi != nil {
		body.Poi = *req.Poi
	}
	if req.DeviceModel != nil {
		body.DeviceModel = *req.DeviceModel
	}
	if req.DeviceSystem != nil {
		body.DeviceSystem = *req.DeviceSystem
	}

	// Always include the request body in the response so admin can confirm
	// what hit the wire. Marshal once for both diagnostics and the actual call.
	bodyJSON, _ := json.MarshalIndent(body, "", "  ")

	out := map[string]any{
		"sentRequest": json.RawMessage(bodyJSON),
		"dryRun":      req.DryRun,
	}

	if req.DryRun {
		writeJSON(w, http.StatusOK, out)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	c := apiclient.New(u.Token)
	data, err := c.Sign(ctx, body)
	if err != nil {
		var ae *apiclient.APIError
		if errors.As(err, &ae) {
			out["ok"] = false
			out["httpStatus"] = ae.HTTPStatus
			out["envelopeCode"] = ae.Code
			out["envelopeMessage"] = ae.Message
			if len(ae.Data) > 0 {
				out["envelopeData"] = json.RawMessage(ae.Data)
			} else {
				out["envelopeData"] = nil
			}
			if len(ae.RawBody) > 0 {
				out["rawBody"] = string(ae.RawBody)
			}
		} else {
			out["ok"] = false
			out["error"] = err.Error()
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["ok"] = true
	out["envelopeCode"] = 200
	out["envelopeMessage"] = "ok"
	if len(data) > 0 {
		out["envelopeData"] = json.RawMessage(data)
	}
	writeJSON(w, http.StatusOK, out)
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
