package main

import (
	"strings"
)

var balancesAction = &Command{
	Name:   "balances",
	Usage:  "request the account's balances over the connection",
	Flags:  accountFlags,
	Action: accountQuery("balances"),
}

var positionsAction = &Command{
	Name:   "positions",
	Usage:  "request the account's positions over the connection",
	Flags:  accountFlags,
	Action: accountQuery("positions"),
}

var accountFlags = []Flag{
	&StringFlag{
		Name:     "account",
		Required: true,
		Usage:    "address of the account to get positions for",
	},
}

// accountQuery sends method for --account, answered under the method's name.
func accountQuery(method string) func(*Context) error {
	return func(c *Context) error {
		return send(JsonRPCRequest{
			JsonRPC: "2.0",
			ID:      method,
			Method:  method,
			Params:  map[string]string{"account": strings.ToLower(c.String("account"))},
		})
	}
}
