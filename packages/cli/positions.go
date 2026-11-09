package main

import (
	"strings"
)

var positionsAction = &Command{
	Name:  "positions",
	Usage: "request the account's positions over the connection",
	Flags: []Flag{
		&StringFlag{
			Name:     "account",
			Required: true,
			Usage:    "address of the account to get positions for",
		},
	},
	Action: func(c *Context) error {
		return positions(c)
	},
}

func positions(c *Context) error {
	account := strings.ToLower(c.String("account"))

	payload := JsonRPCRequest{
		JsonRPC: "2.0",
		ID:      "positions",
		Method:  "positions",
		Params: map[string]string{
			"account": account,
		},
	}

	return send(payload)
}
