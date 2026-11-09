package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const V12_URL = "https://v12.rysk.finance"

var selfRedeemAction = &Command{
	Name:  "self-redeem",
	Usage: "redeem the signer's own expired otokens",
	Flags: []Flag{
		&StringFlag{
			Name:    "url",
			Value:   V12_URL,
			EnvVars: []string{"RYSK_URL"},
			Usage:   "base url of the v12 api",
		},
		&Int64Flag{
			Name:     "chain_id",
			Required: true,
		},
		&StringFlag{
			Name:     "nonce",
			Required: true,
			Usage:    "unused nonce for this account (decimal uint64)",
		},
		&Int64Flag{
			Name:  "expiry",
			Usage: "unix seconds cutoff: otokens expiring at or before it are redeemed. Defaults to now on the server",
		},
		&StringFlag{
			Name:  "exclude_assets",
			Usage: "comma separated asset addresses to leave unredeemed",
		},
		&StringFlag{
			Name:     "private_key",
			Required: true,
			Usage:    "private key of the account redeeming",
			EnvVars:  []string{"RYSK_PRIVATE_KEY"},
		},
	},
	Action: selfRedeem,
}

// selfRedeem posts to cc's /api/redeem/self. The redeemer is whoever signed the
// auth headers - the same Authentication scheme the premium api uses - so the
// server only ever redeems for the caller.
func selfRedeem(c *Context) error {
	nonce := c.String("nonce")
	if err := checkNonce(nonce); err != nil {
		return err
	}
	headers, err := premiumAuthHeaders(c.Int64("chain_id"), nonce, c.String("private_key"))
	if err != nil {
		return err
	}

	query := url.Values{"chainId": {strconv.FormatInt(c.Int64("chain_id"), 10)}}
	if c.IsSet("expiry") {
		query.Set("expiry", strconv.FormatInt(c.Int64("expiry"), 10))
	}
	if assets := c.String("exclude_assets"); assets != "" {
		query.Set("excludeAssets", strings.ToLower(assets))
	}

	client := newPremiumClient(c.String("url"))
	status, body, err := client.do(http.MethodPost, "/api/redeem/self", query, nil, headers)
	if err != nil {
		return err
	}
	trimmed := strings.TrimSpace(string(body))
	if status < 200 || status >= 300 {
		return fmt.Errorf("api returned %d: %s", status, trimmed)
	}
	fmt.Println(trimmed)
	return nil
}
