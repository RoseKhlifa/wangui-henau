package api

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
)

type Rule struct {
	RuleID      int    `json:"ruleId"`
	RuleName    string `json:"ruleName"`
	StartTime   string `json:"startTime"`
	EndTime     string `json:"endTime"`
	Description string `json:"description"`
}

type Status struct {
	CanCheckin       bool            `json:"canCheckin"`
	HasCheckedIn     *bool           `json:"hasCheckedIn"`
	IsExempt         *bool           `json:"isExempt"`
	IsBoarding       bool            `json:"isBoarding"`
	Message          string          `json:"message"`
	ExemptReason     *string         `json:"exemptReason"`
	MinutesRemaining *int            `json:"minutesRemaining"`
	CurrentRule      *Rule           `json:"currentRule"`
	TodayRecord      json.RawMessage `json:"todayRecord"`
}

// AvailableRules lists rules currently applicable to the authenticated user.
func (c *Client) AvailableRules(ctx context.Context) ([]Rule, error) {
	var rs []Rule
	if err := c.do(ctx, "GET", "/checkin/available-rules", nil, nil, &rs); err != nil {
		return nil, err
	}
	return rs, nil
}

// CheckinStatus returns the current checkin status for the given rule.
func (c *Client) CheckinStatus(ctx context.Context, ruleID int) (*Status, error) {
	q := url.Values{"ruleId": []string{strconv.Itoa(ruleID)}}
	var s Status
	if err := c.do(ctx, "GET", "/checkin/status", q, nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// SignRequest mirrors the body sent by the frontend on POST /checkin.
//
// Address fields use omitempty: when blank they are omitted from the request body,
// matching the "minimal payload" mode an admin can choose per dorm.
//
// 2026-05 the school added a geofence and started rejecting our requests with
// "当前位置不在签到围栏范围内". The lower block is "extension" fields the school
// may have added — wechat JSAPI getLocation typically returns accuracy +
// altitude + speed, so a stricter server-side validator might require them.
// All marked omitempty so leaving them at zero changes nothing vs the old
// request shape.
type SignRequest struct {
	RuleID          int     `json:"ruleId"`
	Latitude        float64 `json:"latitude"`
	Longitude       float64 `json:"longitude"`
	DeviceModel     string  `json:"deviceModel,omitempty"`
	DeviceSystem    string  `json:"deviceSystem,omitempty"`
	LocationAddress string  `json:"locationAddress,omitempty"`
	City            string  `json:"city,omitempty"`
	Road            string  `json:"road,omitempty"`
	Poi             string  `json:"poi,omitempty"`

	// --- speculative fields added 2026-05 for geofence debugging ---
	// Accuracy in metres of the GPS fix. wx.getLocation defaults to single-digit
	// metres on real devices; a too-large value may itself trigger the fence
	// rejection on the new strict implementation.
	Accuracy float64 `json:"accuracy,omitempty"`
	// Altitude / speed from wx.getLocation. Zero values are dropped by omitempty.
	Altitude float64 `json:"altitude,omitempty"`
	Speed    float64 `json:"speed,omitempty"`
	// CoordType declares which coordinate system Latitude/Longitude are in.
	// wx.getLocation can be called with type "wgs84" or "gcj02"; we typically
	// pass through whatever admin saved on the dorm. Empty = unspecified.
	CoordType string `json:"coordType,omitempty"`
	// Timestamp (ms since epoch) when the GPS fix was captured.
	Timestamp int64 `json:"timestamp,omitempty"`
}

// Sign performs a checkin. Returns the raw `data` payload on success.
func (c *Client) Sign(ctx context.Context, req SignRequest) (json.RawMessage, error) {
	var data json.RawMessage
	if err := c.do(ctx, "POST", "/checkin", nil, req, &data); err != nil {
		return nil, err
	}
	return data, nil
}
