package main

import (
	"strings"
)

var balancesAction = &Command{
	Name:  "balances",
	Usage: "request the account's balances over the connection",
	Flags: []Flag{
		&StringFlag{
			Name:     "account",
			Required: true,
			Usage:    "address of the account to get positions for",
		},
	},
	Action: func(c *Context) error {
		return balances(c)
	},
}

func balances(c *Context) error {
	account := strings.ToLower(c.String("account"))

	payload := JsonRPCRequest{
		JsonRPC: "2.0",
		ID:      "balances",
		Method:  "balances",
		Params: map[string]string{
			"account": account,
		},
	}

	return send(payload)
}
