package main

import "fmt"

var willRedeemAction = &Command{
	Name:  "will_redeem",
	Usage: "opt an otoken in or out of redemption at expiry",
	Flags: []Flag{
		&IntFlag{
			Name:     "chain_id",
			Required: true,
		},
		&StringFlag{
			Name:     "account",
			Required: true,
			Usage:    "maker address, has to match the signing key",
		},
		&StringFlag{
			Name:     "otoken",
			Required: true,
			Usage:    "otoken address",
		},
		&BoolFlag{
			Name:  "will_redeem",
			Usage: "present to have the otoken redeemed at expiry, left out to keep the balance",
		},
		&Uint64Flag{
			Name:     "nonce",
			Required: true,
			Usage:    "unused nonce for this account (stringified u64)",
		},
		&StringFlag{
			Name:     "private_key",
			Required: true,
			Usage:    "private key to sign messages with",
			EnvVars:  []string{"RYSK_PRIVATE_KEY"},
		},
	},
	Action: func(c *Context) error {
		w := WillRedeem{
			Account:    c.String("account"),
			Otoken:     c.String("otoken"),
			ChainID:    c.Int("chain_id"),
			WillRedeem: c.Bool("will_redeem"),
			Nonce:      fmt.Sprintf("%d", c.Uint64("nonce")),
		}
		msgHash, err := CreateWillRedeemMessage(w)
		if err != nil {
			return err
		}
		if w.Signature, err = Sign(msgHash, c.String("private_key")); err != nil {
			return err
		}
		return send(JsonRPCRequest{JsonRPC: "2.0", ID: "will_redeem", Method: "will_redeem", Params: w})
	},
}
