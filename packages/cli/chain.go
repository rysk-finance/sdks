package main

import (
	"context"
	"crypto/ecdsa"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	crypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	CHAIN_ID_ETH           int = 1
	CHAIN_ID_ETH_SEPOLIA   int = 11155111
	CHAIN_ID_BASE          int = 8453
	CHAIN_ID_HYPE          int = 999
	CHAIN_ID_BASE_SEPOLIA  int = 84532
	CHAIN_ID_HYPE_TESTNET  int = 998
	CHAIN_ID_MONAD_TESTNET int = 10143
)

type Addresses struct {
	MMarket     common.Address
	Rysk        common.Address
	StrikeAsset common.Address
}

type Account struct {
	Public  common.Address
	Private *ecdsa.PrivateKey
}

var ADDRESSES = map[int]Addresses{
	CHAIN_ID_ETH: {
		Rysk:        common.HexToAddress("0x7a3ddeac7a0ae6dfa9391c764499a3564f3c2aad"),
		MMarket:     common.HexToAddress("0xc01c9ef5de5862354add9501a29e8765cff01c32"),
		StrikeAsset: common.HexToAddress("0xdac17f958d2ee523a2206206994597c13d831ec7"),
	},
	CHAIN_ID_ETH_SEPOLIA: {
		Rysk:        common.HexToAddress("0x166e38f44EBd5e82E9a4095e5FC24D52D6F01617"),
		MMarket:     common.HexToAddress("0x27809a3Bd3cf44d855f1BE668bFD16D34bcE157C"),
		StrikeAsset: common.HexToAddress("0xdA6c2B2BB2296a403D97a7D1F4e064C99a0e6A9A"),
	},
	CHAIN_ID_BASE_SEPOLIA: {
		Rysk:        common.HexToAddress("0x0ff34dd648b68f09b199b60b91442e750fd13fdc"),
		MMarket:     common.HexToAddress("0x6d23217cb228f3779f6506b44b1abaa3edf6fc58"),
		StrikeAsset: common.HexToAddress("0x98d56648c9b7f3cb49531f4135115b5000ab1733"),
	},
	CHAIN_ID_MONAD_TESTNET: {
		Rysk:        common.HexToAddress("0x3b87932046bc3e1bb63c13aba1306f9d398a9cc6"),
		MMarket:     common.HexToAddress("0x0d71dbdb11c35c72fa4a33beec00d5eb1fbcf1c8"),
		StrikeAsset: common.HexToAddress("0xf817257fed379853cde0fa4f97ab987181b1e5ea"),
	},
	CHAIN_ID_HYPE_TESTNET: {
		Rysk:        common.HexToAddress("0x0122db8Ed9B9B49F3FD8774d93773C2e2A564E81"),
		MMarket:     common.HexToAddress("0x4bB4dDf8F4588780fe0cD31593bd8f5719eBfefd"),
		StrikeAsset: common.HexToAddress("0x69E37eCb273a23B8864A5f39520Bf97c6768b65C"),
	},
	CHAIN_ID_BASE: {
		Rysk:        common.HexToAddress("0xe33a517dfef3d582f6eb94276e8d514f835a1401"),
		MMarket:     common.HexToAddress("0x3D9CB5D2Fa4600bF8d75fB59Fe01Db765dCced15"),
		StrikeAsset: common.HexToAddress("0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"),
	},
	CHAIN_ID_HYPE: {
		Rysk:        common.HexToAddress("0x8c8bcb6d2c0e31c5789253ecc8431ca6209b4e35"),
		MMarket:     common.HexToAddress("0x691a5fc3a81a144e36c6C4fBCa1fC82843c80d0d"),
		StrikeAsset: common.HexToAddress("0xB8CE59FC3717ada4C02eaDF9682A9e934F625ebb"),
	},
}

func newAccountFromPrivateKey(pk string) (account Account, err error) {
	account = Account{}
	account.Private, err = crypto.HexToECDSA(strings.TrimPrefix(pk, "0x")) // hex w/o 0x
	if err != nil {
		return account, err
	}

	account.Public = crypto.PubkeyToAddress(account.Private.PublicKey)
	return account, nil

}

func (a *Account) approve(ctx context.Context, chainID int, client ethclient.Client, amount *big.Int, asset common.Address, spender common.Address) (txHash string, err error) {
	// bind fills in the nonce; the gas price is set so the tx stays legacy
	opts, err := bind.NewKeyedTransactorWithChainID(a.Private, big.NewInt(int64(chainID)))
	if err != nil {
		return "", err
	}
	if opts.GasPrice, err = client.SuggestGasPrice(ctx); err != nil {
		return "", err
	}

	tx, err := erc20(asset, &client).Transact(opts, "approve", spender, amount)
	if err != nil {
		return "", err
	}

	rx, err := bind.WaitMined(ctx, &client, tx)
	if err != nil {
		return "", err
	}

	return rx.TxHash.Hex(), nil
}
