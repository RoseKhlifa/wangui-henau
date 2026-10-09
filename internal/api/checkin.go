package api

import (
	"context"
	"crypto/aes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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
	IsCampusNetwork  *bool           `json:"isCampusNetwork"`
	Message          string          `json:"message"`
	ExemptReason     *string         `json:"exemptReason"`
	MinutesRemaining *int            `json:"minutesRemaining"`
	CurrentRule      *Rule           `json:"currentRule"`
	TodayRecord      json.RawMessage `json:"todayRecord"`
}

// AvailableRules lists rules currently applicable to the authenticated user.
func (c *Client) AvailableRules(ctx context.Context) ([]Rule, error) {
	var rs []Rule
	if err := c.do(ctx, http.MethodGet, "/checkin/available-rules", nil, nil, &rs); err != nil {
		return nil, err
	}
	return rs, nil
}

// CurrentRule returns the first rule exposed by the school for this user.
// The school controls this list, so callers must not assume that rule 1 is
// permanently active.
func (c *Client) CurrentRule(ctx context.Context) (*Rule, error) {
	rules, err := c.AvailableRules(ctx)
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return nil, errors.New("school returned no available check-in rule")
	}
	return &rules[0], nil
}

// CheckinStatus returns the current check-in status for the given rule.
func (c *Client) CheckinStatus(ctx context.Context, ruleID int) (*Status, error) {
	q := url.Values{"ruleId": []string{strconv.Itoa(ruleID)}}
	var s Status
	if err := c.do(ctx, http.MethodGet, "/checkin/status", q, nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// SignRequest contains the values protected by the school's current signing
// protocol. LocationAddress is required because it is part of the canonical
// signature input even when other address metadata is not transmitted.
type SignRequest struct {
	RuleID          int
	Latitude        float64
	Longitude       float64
	DeviceModel     string
	DeviceSystem    string
	LocationAddress string
}

type prepareData struct {
	MaskedSignKey string `json:"a"`
	MaskedAESKey  string `json:"b"`
	Nonce         string `json:"nonce"`
}

type encryptedSignEnvelope struct {
	Param string `json:"param"`
	Nonce string `json:"nonce"`
}

type signedCheckinPayload struct {
	DeviceModel     string `json:"deviceModel"`
	DeviceSystem    string `json:"deviceSystem"`
	Latitude        string `json:"latitude"`
	LocationAddress string `json:"locationAddress"`
	Longitude       string `json:"longitude"`
	RuleID          string `json:"ruleId"`
	TimeMillis      string `json:"timeMillis"`
	Sign            string `json:"sign"`
}

func (c *Client) prepareCheckin(ctx context.Context) (*prepareData, error) {
	var out prepareData
	if err := c.do(ctx, http.MethodGet, "/checkin/prepare", nil, nil, &out); err != nil {
		return nil, err
	}
	if strings.TrimSpace(out.Nonce) == "" {
		return nil, errors.New("check-in prepare response has an empty nonce")
	}
	return &out, nil
}

// Sign performs one check-in attempt using a fresh prepare response and nonce.
// Transient keys, canonical plaintext, signature, and ciphertext are never
// exposed to callers or logs.
func (c *Client) Sign(ctx context.Context, req SignRequest) (json.RawMessage, error) {
	if req.RuleID <= 0 {
		return nil, errors.New("check-in rule ID is required")
	}
	if strings.TrimSpace(req.LocationAddress) == "" {
		return nil, errors.New("check-in location address is required")
	}

	prepare, err := c.prepareCheckin(ctx)
	if err != nil {
		return nil, fmt.Errorf("prepare check-in: %w", err)
	}
	envelope, err := buildEncryptedSignEnvelope(req, *prepare, time.Now().UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("build encrypted check-in request: %w", err)
	}

	var data json.RawMessage
	if err := c.do(ctx, http.MethodPost, "/checkin", nil, envelope, &data); err != nil {
		return nil, err
	}
	return data, nil
}

func buildEncryptedSignEnvelope(req SignRequest, prepare prepareData, timeMillis int64) (*encryptedSignEnvelope, error) {
	signKey, err := unmaskPrepareValue(prepare.MaskedSignKey)
	if err != nil {
		return nil, fmt.Errorf("decode signing key: %w", err)
	}
	aesKeyText, err := unmaskPrepareValue(prepare.MaskedAESKey)
	if err != nil {
		return nil, fmt.Errorf("decode encryption key: %w", err)
	}
	aesKey, err := base64.StdEncoding.DecodeString(aesKeyText)
	if err != nil {
		return nil, errors.New("prepare encryption key is not valid base64")
	}
	if len(aesKey) != 16 && len(aesKey) != 24 && len(aesKey) != 32 {
		return nil, errors.New("prepare encryption key has an invalid AES length")
	}
	if strings.TrimSpace(prepare.Nonce) == "" {
		return nil, errors.New("prepare nonce is empty")
	}

	payload := signedCheckinPayload{
		DeviceModel:     req.DeviceModel,
		DeviceSystem:    req.DeviceSystem,
		Latitude:        strconv.FormatFloat(req.Latitude, 'g', -1, 64),
		LocationAddress: req.LocationAddress,
		Longitude:       strconv.FormatFloat(req.Longitude, 'g', -1, 64),
		RuleID:          strconv.Itoa(req.RuleID),
		TimeMillis:      strconv.FormatInt(timeMillis, 10),
	}
	canonical := canonicalSignSource(payload, signKey)
	digest := md5.Sum([]byte(canonical))
	payload.Sign = strings.ToUpper(hex.EncodeToString(digest[:]))

	plain, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal signed payload: %w", err)
	}
	ciphertext, err := encryptAESECBPKCS7(plain, aesKey)
	if err != nil {
		return nil, err
	}
	return &encryptedSignEnvelope{
		Param: base64.StdEncoding.EncodeToString(ciphertext),
		Nonce: prepare.Nonce,
	}, nil
}

func canonicalSignSource(p signedCheckinPayload, signKey string) string {
	return "deviceModel=" + p.DeviceModel +
		"&deviceSystem=" + p.DeviceSystem +
		"&latitude=" + p.Latitude +
		"&locationAddress=" + p.LocationAddress +
		"&longitude=" + p.Longitude +
		"&ruleId=" + p.RuleID +
		"&timeMillis=" + p.TimeMillis +
		"&key=" + signKey
}

func unmaskPrepareValue(masked string) (string, error) {
	chars := []rune(masked)
	if len(chars) < 8 {
		return "", errors.New("masked prepare value is too short")
	}
	var b strings.Builder
	for i, char := range chars {
		if i == 1 || i == 4 || i == 7 {
			continue
		}
		b.WriteRune(char)
	}
	if b.Len() == 0 {
		return "", errors.New("unmasked prepare value is empty")
	}
	return b.String(), nil
}

func encryptAESECBPKCS7(plain, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	blockSize := block.BlockSize()
	padding := blockSize - len(plain)%blockSize
	padded := make([]byte, len(plain)+padding)
	copy(padded, plain)
	for i := len(plain); i < len(padded); i++ {
		padded[i] = byte(padding)
	}
	ciphertext := make([]byte, len(padded))
	for offset := 0; offset < len(padded); offset += blockSize {
		block.Encrypt(ciphertext[offset:offset+blockSize], padded[offset:offset+blockSize])
	}
	return ciphertext, nil
}
