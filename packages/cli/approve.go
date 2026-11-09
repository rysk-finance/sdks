package main

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

var approveAction = &Command{
	Name:  "approve",
	Usage: "approve spending of default strike asset",
	Flags: []Flag{
		&Int64Flag{
			Name:     "chain_id",
			Required: true,
			Usage:    "chain_id",
		},
		&StringFlag{
			Name:     "rpc_url",
			Required: true,
			Usage:    "rpc url",
		},
		&StringFlag{
			Name:     "amount",
			Required: true,
			Usage:    "amount to approve",
		},
		&StringFlag{
			Name:     "private_key",
			Required: true,
			Usage:    "private key of approving account",
			EnvVars:  []string{"RYSK_PRIVATE_KEY"},
		},
		&StringFlag{
			Name:     "asset",
			Required: false,
			Usage:    "address of the asset to approve (defaults to the chain's strike asset)",
		},
		&StringFlag{
			Name:  "spender",
			Usage: "address allowed to spend it (defaults to the chain's mmarket, which maker deposits pull through; a taker's collateral goes to the margin pool)",
		},
	},
	Action: func(c *Context) error {
		return approve(c)
	},
}

func approve(c *Context) error {
	chain_id := c.Int("chain_id")
	rpc_url := c.String("rpc_url")
	amount := c.String("amount")
	pk := c.String("private_key")

	asset := ADDRESSES[chain_id].StrikeAsset
	if assetArg := c.String("asset"); assetArg != "" {
		asset = common.HexToAddress(assetArg)
	}

	account, err := newAccountFromPrivateKey(pk)
	if err != nil {
		return err
	}

	bigAmount, ok := new(big.Int).SetString(amount, 10)
	if !ok {
		return fmt.Errorf("%s cannot be turned into a big.Int", amount)
	}

	client, err := ethclient.DialContext(c.Context, rpc_url)
	if err != nil {
		return err
	}

	spender := ADDRESSES[chain_id].MMarket
	if spenderArg := c.String("spender"); spenderArg != "" {
		if !common.IsHexAddress(spenderArg) {
			return fmt.Errorf("invalid spender %q", spenderArg)
		}
		spender = common.HexToAddress(spenderArg)
	}

	txHash, err := account.approve(c.Context, chain_id, *client, bigAmount, asset, spender)
	if err != nil {
		return err
	}

	fmt.Println(txHash)
	return nil
}
