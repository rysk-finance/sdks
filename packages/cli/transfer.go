package main

import (
	"fmt"
	"time"
)

var transferAction = &Command{
	Name:  "transfer",
	Usage: "request a transfer",
	Flags: []Flag{
		&Int64Flag{
			Name:     "chain_id",
			Required: true,
			Usage:    "chain_id",
		},
		&StringFlag{
			Name:     "asset",
			Required: true,
			Usage:    "asset address",
		},
		&StringFlag{
			Name:     "user",
			Required: true,
			Usage:    "user address",
		},
		&StringFlag{
			Name:     "amount",
			Required: true,
			Usage:    "amount to deposit",
		},
		&BoolFlag{
			Name:  "is_deposit",
			Usage: "whether you want to deposit or withdraw",
		},
		&Uint64Flag{
			Name:     "nonce",
			Required: true,
			Usage:    "nonce to sign the message with",
		},
		&StringFlag{
			Name:     "private_key",
			Required: true,
			Usage:    "private key to sign messages with",
			EnvVars:  []string{"RYSK_PRIVATE_KEY"},
		},
	},
	Action: func(c *Context) error {
		return transfer(c)
	},
}

func transfer(c *Context) error {
	method := "withdraw"
	if c.Bool("is_deposit") {
		method = "deposit"
	}

	payload := JsonRPCRequest{
		JsonRPC: "2.0",
		ID:      fmt.Sprintf("%d", time.Now().Unix()),
		Method:  method,
	}

	t := Transfer{
		User:      c.String("user"),
		Asset:     c.String("asset"),
		ChainID:   int(c.Int64("chain_id")),
		Amount:    c.String("amount"),
		IsDeposit: c.Bool("is_deposit"),
		Nonce:     fmt.Sprintf("%d", c.Uint64("nonce")),
	}

	msgHash, _, err := CreateTransferMessage(t)
	if err != nil {
		return err
	}
	sig, err := Sign(msgHash, c.String("private_key"))
	if err != nil {
		return err
	}
	t.Signature = sig
	payload.Params = t
	return send(payload)
}
