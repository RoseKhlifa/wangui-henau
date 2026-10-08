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
// A historical deployment returned "当前位置不在签到围栏范围内" when the
// application server used an overseas egress IP far from the submitted
// location. The lower block contains experimental diagnostic fields; none has
// been confirmed as required by the school API. All use omitempty so leaving
// them at zero preserves the established request shape.
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

	// --- unconfirmed diagnostic fields; not used by the production scheduler ---
	// Accuracy in metres of the GPS fix. This field is unconfirmed and retained
	// only for controlled diagnostics.
	Accuracy float64 `json:"accuracy,omitempty"`
	// Altitude / speed from wx.getLocation. Zero values are dropped by omitempty.
	Altitude float64 `json:"altitude,omitempty"`
	Speed    float64 `json:"speed,omitempty"`
	// CoordType declares which coordinate system Latitude/Longitude are in.
	// Whether the school API consumes it is unconfirmed. Empty = unspecified.
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
