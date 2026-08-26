package main

import (
	"strings"

	"github.com/urfave/cli/v2"
)

var positionsAction = &cli.Command{
	Name: "positions",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "channel_id",
			Required: true,
			Usage:    "the socket id to send messages into",
		},
		&cli.StringFlag{
			Name:     "account",
			Required: true,
			Usage:    "address of the account to get positions for",
		},
	},
	Action: func(c *cli.Context) error {
		return positions(c)
	},
}

func positions(c *cli.Context) error {
	account := strings.ToLower(c.String("account"))

	payload := JsonRPCRequest{
		JsonRPC: "2.0",
		ID:      "positions",
		Method:  "positions",
		Params: map[string]string{
			"account": account,
		},
	}

	return writeToSocket(c.String("channel_id"), payload)
}
