// signprobe — one-shot debug script for the 2026-05 geofence rejection.
//
// Usage:
//   go run ./cmd/signprobe -code <oauth_code>
//   go run ./cmd/signprobe -token <existing_jwt>
//
// Exchanges a fresh wechat-OAuth code for the school's JWT (or reuses one),
// queries /auth/user + /checkin/available-rules, then hammers /checkin with
// several payload shapes, printing the full response body for each so we can
// figure out which field combination the new server-side validator wants.
//
// Doesn't write to wangui's DB. Doesn't send notifications. Pure curl-with-
// preset.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"wangui/internal/api"
)

func main() {
	codeFlag := flag.String("code", "", "WeChat OAuth code (one-shot, ~5min lifetime)")
	tokFlag := flag.String("token", "", "Pre-exchanged school JWT (bypass OAuth)")
	latFlag := flag.Float64("lat", 0, "Latitude (overrides default)")
	lngFlag := flag.Float64("lng", 0, "Longitude (overrides default)")
	addrFlag := flag.String("addr", "", "locationAddress for the address-fields preset")
	cityFlag := flag.String("city", "", "city")
	roadFlag := flag.String("road", "", "road")
	poiFlag := flag.String("poi", "", "poi")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var token string
	if *tokFlag != "" {
		token = strings.TrimSpace(*tokFlag)
	} else if *codeFlag != "" {
		fmt.Println("== exchanging OAuth code →  JWT ==")
		c := api.New("")
		resp, err := c.OAuth2Login(ctx, strings.TrimSpace(*codeFlag))
		if err != nil {
			die("oauth exchange failed: %v", err)
		}
		token = strings.TrimSpace(resp.AccessToken)
		fmt.Printf("  isNewUser: %v\n", resp.IsNewUser)
		fmt.Printf("  token len: %d\n", len(token))
	} else {
		die("must supply -code or -token")
	}
	if token == "" {
		die("no token resolved")
	}

	c := api.New(token)

	fmt.Println("\n== /auth/user ==")
	u, err := c.GetUser(ctx)
	if err != nil {
		die("get-user: %v", err)
	}
	// Avatar is data: URI — too long, trim.
	if len(u.UserAvatarURL) > 80 {
		u.UserAvatarURL = u.UserAvatarURL[:80] + "...(trimmed)"
	}
	printJSON("user", u)

	fmt.Println("\n== /checkin/available-rules ==")
	rules, err := c.AvailableRules(ctx)
	if err != nil {
		fmt.Printf("  err: %v\n", err)
	} else {
		printJSON("rules", rules)
	}

	ruleID := 1
	if len(rules) > 0 {
		ruleID = rules[0].RuleID
	}

	fmt.Println("\n== /checkin/status ==")
	st, err := c.CheckinStatus(ctx, ruleID)
	if err != nil {
		fmt.Printf("  err: %v\n", err)
	} else {
		printJSON("status", st)
	}

	// Coords default to the user's most recent guess; override via flags.
	lat := *latFlag
	lng := *lngFlag
	if lat == 0 && lng == 0 {
		fmt.Println("\n!! lat/lng not supplied via -lat/-lng. Will skip /checkin probes.")
		fmt.Println("   Re-run with at least one of -lat/-lng pointing at the dorm.")
		return
	}

	type Probe struct {
		Name string
		Req  api.SignRequest
	}
	probes := []Probe{
		{
			Name: "A. 裸最小（当前 production 行为）",
			Req: api.SignRequest{
				RuleID:       ruleID,
				Latitude:     lat,
				Longitude:    lng,
				DeviceModel:  "iPhone",
				DeviceSystem: "iOS",
			},
		},
		{
			Name: "B. + accuracy=15",
			Req: api.SignRequest{
				RuleID:       ruleID,
				Latitude:     lat,
				Longitude:    lng,
				DeviceModel:  "iPhone",
				DeviceSystem: "iOS",
				Accuracy:     15,
			},
		},
		{
			Name: "C. + accuracy=15 + altitude=85 + speed=0 + timestamp=now",
			Req: api.SignRequest{
				RuleID:       ruleID,
				Latitude:     lat,
				Longitude:    lng,
				DeviceModel:  "iPhone",
				DeviceSystem: "iOS",
				Accuracy:     15,
				Altitude:     85,
				Speed:        0,
				Timestamp:    time.Now().UnixMilli(),
			},
		},
		{
			Name: "D. + coordType=wgs84",
			Req: api.SignRequest{
				RuleID:       ruleID,
				Latitude:     lat,
				Longitude:    lng,
				DeviceModel:  "iPhone",
				DeviceSystem: "iOS",
				CoordType:    "wgs84",
			},
		},
		{
			Name: "E. + coordType=gcj02",
			Req: api.SignRequest{
				RuleID:       ruleID,
				Latitude:     lat,
				Longitude:    lng,
				DeviceModel:  "iPhone",
				DeviceSystem: "iOS",
				CoordType:    "gcj02",
			},
		},
		{
			Name: "F. + locationAddress / city / road / poi（用户猜测）",
			Req: api.SignRequest{
				RuleID:          ruleID,
				Latitude:        lat,
				Longitude:       lng,
				DeviceModel:     "iPhone",
				DeviceSystem:    "iOS",
				LocationAddress: *addrFlag,
				City:            *cityFlag,
				Road:            *roadFlag,
				Poi:             *poiFlag,
			},
		},
		{
			Name: "G. 全套（C + F）",
			Req: api.SignRequest{
				RuleID:          ruleID,
				Latitude:        lat,
				Longitude:       lng,
				DeviceModel:     "iPhone",
				DeviceSystem:    "iOS",
				Accuracy:        15,
				Altitude:        85,
				Speed:           0,
				Timestamp:       time.Now().UnixMilli(),
				LocationAddress: *addrFlag,
				City:            *cityFlag,
				Road:            *roadFlag,
				Poi:             *poiFlag,
			},
		},
	}

	for _, p := range probes {
		fmt.Printf("\n=========== %s ===========\n", p.Name)
		body, _ := json.MarshalIndent(p.Req, "", "  ")
		fmt.Println("→ request:")
		fmt.Println(indent(string(body), "  "))

		data, err := c.Sign(ctx, p.Req)
		if err != nil {
			var ae *api.APIError
			if errors.As(err, &ae) {
				fmt.Printf("← FAILED  http=%d  code=%d  msg=%q\n", ae.HTTPStatus, ae.Code, ae.Message)
				if len(ae.Data) > 0 {
					fmt.Println("  envelope.data:")
					fmt.Println(indent(prettyJSON(ae.Data), "  "))
				}
				if len(ae.RawBody) > 0 {
					fmt.Println("  raw:")
					fmt.Println(indent(string(ae.RawBody), "  "))
				}
			} else {
				fmt.Printf("← FAILED  err=%v\n", err)
			}
			continue
		}
		fmt.Println("← OK  envelope.data:")
		fmt.Println(indent(prettyJSON(data), "  "))
		fmt.Println("\n!!! 学校接受了这个 payload !!! 后续 probe 没必要继续，停止。")
		return
	}

	fmt.Println("\n所有 probe 全部失败。")
}

func printJSON(label string, v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Printf("  %s:\n%s\n", label, indent(string(b), "    "))
}

func prettyJSON(raw json.RawMessage) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func indent(s, pad string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FATAL: "+format+"\n", args...)
	os.Exit(1)
}
