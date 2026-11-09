package main

import (
	"context"
	"log"
	"os"
)

// updated at build time
var Version = ""

var app = &Command{
	Name: "ryskV12",
	Subcommands: []*Command{
		approveAction,
		balancesAction,
		confirmAction,
		connectAction,
		inventoryAction,
		positionsAction,
		premiumAction,
		quoteAction,
		requestAction,
		selfRedeemAction,
		transferAction,
		versionAction,
		willRedeemAction,
	},
}

func main() {
	if err := app.Run(context.Background(), os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
