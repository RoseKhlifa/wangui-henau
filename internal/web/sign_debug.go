package web

import (
	"context"
	"encoding/json"
	"errors"
	"math"
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

// POST /api/v1/rosekhlifa/users/{id}/sign-debug-batch
//
// Iterate through 7 preset payload shapes back-to-back from the wangui
// server side (so the school sees a Chinese IP, not the sandbox).
// Returns an array of {label, sentRequest, response...} so the admin UI
// can show all 7 results in one table.
//
// Body (all optional, same as sign-debug):
//   { latitude, longitude, locationAddress, city, road, poi }
//
// Stops on first success to avoid wasting attempts (the school may have
// per-account rate limits we don't know about).
type batchProbeReq struct {
	Latitude        *float64 `json:"latitude"`
	Longitude       *float64 `json:"longitude"`
	LocationAddress *string  `json:"locationAddress"`
	City            *string  `json:"city"`
	Road            *string  `json:"road"`
	Poi             *string  `json:"poi"`
}

type batchProbeResult struct {
	Label           string          `json:"label"`
	SentRequest     json.RawMessage `json:"sentRequest"`
	Ok              bool            `json:"ok"`
	HTTPStatus      int             `json:"httpStatus,omitempty"`
	EnvelopeCode    int             `json:"envelopeCode,omitempty"`
	EnvelopeMessage string          `json:"envelopeMessage,omitempty"`
	EnvelopeData    json.RawMessage `json:"envelopeData,omitempty"`
	RawBody         string          `json:"rawBody,omitempty"`
	Error           string          `json:"error,omitempty"`
}

func (h *handlers) adminSignDebugBatch(w http.ResponseWriter, r *http.Request) {
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
	var req batchProbeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		writeErr(w, http.StatusBadRequest, "请求格式错误: "+err.Error())
		return
	}

	lat := u.Lat
	lng := u.Lng
	if req.Latitude != nil {
		lat = *req.Latitude
	}
	if req.Longitude != nil {
		lng = *req.Longitude
	}
	addr := u.Address
	city := u.City
	road := u.Road
	poi := u.Poi
	if req.LocationAddress != nil {
		addr = *req.LocationAddress
	}
	if req.City != nil {
		city = *req.City
	}
	if req.Road != nil {
		road = *req.Road
	}
	if req.Poi != nil {
		poi = *req.Poi
	}

	base := apiclient.SignRequest{
		RuleID:       scheduler.DefaultRuleID,
		Latitude:     lat,
		Longitude:    lng,
		DeviceModel:  nonEmpty(u.DeviceModel, "iPhone"),
		DeviceSystem: nonEmpty(u.DeviceSystem, "iOS"),
	}

	type preset struct {
		label string
		mutate func(*apiclient.SignRequest)
	}
	presets := []preset{
		{"A. 裸最小（当前 production）", func(r *apiclient.SignRequest) {}},
		{"B. + accuracy=15", func(r *apiclient.SignRequest) {
			r.Accuracy = 15
		}},
		{"C. + 完整 wx.getLocation (accuracy/altitude/speed/timestamp)", func(r *apiclient.SignRequest) {
			r.Accuracy = 13
			r.Altitude = 85
			r.Speed = 0
			r.Timestamp = time.Now().UnixMilli()
		}},
		{"D. + coordType=wgs84", func(r *apiclient.SignRequest) {
			r.CoordType = "wgs84"
		}},
		{"E. + coordType=gcj02", func(r *apiclient.SignRequest) {
			r.CoordType = "gcj02"
		}},
		{"F. + locationAddress / city / road / poi", func(r *apiclient.SignRequest) {
			r.LocationAddress = addr
			r.City = city
			r.Road = road
			r.Poi = poi
		}},
		{"G. 全套（C + E + F）", func(r *apiclient.SignRequest) {
			r.Accuracy = 13
			r.Altitude = 85
			r.Speed = 0
			r.Timestamp = time.Now().UnixMilli()
			r.CoordType = "gcj02"
			r.LocationAddress = addr
			r.City = city
			r.Road = road
			r.Poi = poi
		}},
	}

	results := make([]batchProbeResult, 0, len(presets))
	for _, p := range presets {
		body := base
		p.mutate(&body)
		jsonBody, _ := json.MarshalIndent(body, "", "  ")
		res := batchProbeResult{
			Label:       p.label,
			SentRequest: json.RawMessage(jsonBody),
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		c := apiclient.New(u.Token)
		data, err := c.Sign(ctx, body)
		cancel()
		if err != nil {
			var ae *apiclient.APIError
			if errors.As(err, &ae) {
				res.HTTPStatus = ae.HTTPStatus
				res.EnvelopeCode = ae.Code
				res.EnvelopeMessage = ae.Message
				if len(ae.Data) > 0 {
					res.EnvelopeData = json.RawMessage(ae.Data)
				}
				if len(ae.RawBody) > 0 {
					res.RawBody = string(ae.RawBody)
				}
			} else {
				res.Error = err.Error()
			}
			results = append(results, res)
			continue
		}
		res.Ok = true
		res.HTTPStatus = 200
		res.EnvelopeCode = 200
		res.EnvelopeMessage = "ok"
		if len(data) > 0 {
			res.EnvelopeData = json.RawMessage(data)
		}
		results = append(results, res)
		// Stop on first success — the school may have rate limits and
		// we found the magic combo. The frontend marks the stopping
		// preset as "✅ FOUND" and remaining as skipped.
		break
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"results": results,
		"total":   len(presets),
	})
}

// POST /api/v1/rosekhlifa/users/{id}/sign-debug-sweep
//
// Localize the school's geofence by sweeping coordinates. School now shows
// a 10m accuracy indicator on its H5 frontend — likely the geofence is
// just a tight circle around the dorm's true coordinates. Our saved
// coords might be off by tens of metres (WGS84 vs GCJ02 offset, or an
// imprecise pin on the map).
//
// We sweep an outward spiral: center, then 8 compass points at radius
// 50m, 200m, 500m (24 outer points + 1 center = 25 attempts). Stop on
// first success — that point is "close enough" to the real geofence,
// and admin can refine from there.
//
// Conversion: 1° latitude ≈ 111,320m everywhere. 1° longitude depends
// on latitude: ≈ 111,320 * cos(lat°). At 河南 (~34°) that's ~92,300m.
//
// Body: same as batch — { latitude, longitude } override the center.
// Address fields aren't included; this probe is coordinate-only.
type sweepReq struct {
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
	// Custom radii in metres. nil/empty → default [50,200,500]. Caller can
	// pass [500,2000,10000] for wide sweep when close sweep returns all
	// "不在围栏内" (i.e. the geofence is farther than we guessed).
	Radii []int `json:"radii"`
}

type sweepResult struct {
	Direction       string  `json:"direction"` // N / NE / E / SE / S / SW / W / NW / CENTER
	DistanceM       int     `json:"distanceM"` // 0 for center, else meters
	Latitude        float64 `json:"latitude"`
	Longitude       float64 `json:"longitude"`
	Ok              bool    `json:"ok"`
	HTTPStatus      int     `json:"httpStatus,omitempty"`
	EnvelopeCode    int     `json:"envelopeCode,omitempty"`
	EnvelopeMessage string  `json:"envelopeMessage,omitempty"`
}

func (h *handlers) adminSignDebugSweep(w http.ResponseWriter, r *http.Request) {
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
	var req sweepReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		writeErr(w, http.StatusBadRequest, "请求格式错误: "+err.Error())
		return
	}
	centerLat := u.Lat
	centerLng := u.Lng
	if req.Latitude != nil {
		centerLat = *req.Latitude
	}
	if req.Longitude != nil {
		centerLng = *req.Longitude
	}
	if centerLat == 0 && centerLng == 0 {
		writeErr(w, http.StatusBadRequest, "需要中心坐标 (latitude/longitude)")
		return
	}

	// Build sweep grid.
	type point struct {
		dir  string
		dist int
		lat  float64
		lng  float64
	}
	directions := []struct {
		name      string
		dyFactor  float64 // latitude direction (-1..+1)
		dxFactor  float64 // longitude direction (-1..+1)
	}{
		{"N", +1, 0},
		{"NE", +0.7071, +0.7071},
		{"E", 0, +1},
		{"SE", -0.7071, +0.7071},
		{"S", -1, 0},
		{"SW", -0.7071, -0.7071},
		{"W", 0, -1},
		{"NW", +0.7071, -0.7071},
	}
	distances := []int{50, 200, 500} // default metres
	if len(req.Radii) > 0 {
		distances = distances[:0]
		for _, r := range req.Radii {
			if r > 0 && r <= 100000 { // cap at 100km to avoid abuse
				distances = append(distances, r)
			}
		}
		if len(distances) == 0 {
			distances = []int{50, 200, 500}
		}
	}

	mPerDegLat := 111320.0
	mPerDegLng := 111320.0 * math.Cos(centerLat*math.Pi/180)

	points := []point{{"CENTER", 0, centerLat, centerLng}}
	for _, d := range distances {
		for _, dir := range directions {
			dLat := dir.dyFactor * float64(d) / mPerDegLat
			dLng := dir.dxFactor * float64(d) / mPerDegLng
			points = append(points, point{
				dir:  dir.name,
				dist: d,
				lat:  centerLat + dLat,
				lng:  centerLng + dLng,
			})
		}
	}

	// Fire requests sequentially, stop on first ok.
	results := make([]sweepResult, 0, len(points))
	for _, p := range points {
		body := apiclient.SignRequest{
			RuleID:       scheduler.DefaultRuleID,
			Latitude:     p.lat,
			Longitude:    p.lng,
			DeviceModel:  nonEmpty(u.DeviceModel, "iPhone"),
			DeviceSystem: nonEmpty(u.DeviceSystem, "iOS"),
		}
		res := sweepResult{
			Direction: p.dir,
			DistanceM: p.dist,
			Latitude:  p.lat,
			Longitude: p.lng,
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		c := apiclient.New(u.Token)
		_, err := c.Sign(ctx, body)
		cancel()
		if err != nil {
			var ae *apiclient.APIError
			if errors.As(err, &ae) {
				res.HTTPStatus = ae.HTTPStatus
				res.EnvelopeCode = ae.Code
				res.EnvelopeMessage = ae.Message
			} else {
				res.EnvelopeMessage = err.Error()
			}
			results = append(results, res)
			continue
		}
		res.Ok = true
		res.HTTPStatus = 200
		res.EnvelopeCode = 200
		res.EnvelopeMessage = "ok"
		results = append(results, res)
		break // FOUND — stop hammering school API
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"results":     results,
		"total":       len(points),
		"centerLat":   centerLat,
		"centerLng":   centerLng,
	})
}
