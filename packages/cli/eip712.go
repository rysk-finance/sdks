package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	crypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

var ZeroAddress = common.HexToAddress("0x0")

var EIP712_TYPES = &apitypes.Types{
	"EIP712Domain": {
		{
			Name: "name",
			Type: "string",
		},
		{
			Name: "version",
			Type: "string",
		},
		{
			Name: "chainId",
			Type: "uint256",
		},
		{
			Name: "verifyingContract",
			Type: "address",
		},
	},
	"Quote": {
		{
			Name: "assetAddress",
			Type: "address",
		},
		{
			Name: "chainId",
			Type: "uint256",
		},
		{
			Name: "isPut",
			Type: "bool",
		},
		{
			Name: "strike",
			Type: "uint256",
		},
		{
			Name: "expiry",
			Type: "uint64",
		},
		{
			Name: "maker",
			Type: "address",
		},
		{
			Name: "nonce",
			Type: "uint64",
		},
		{
			Name: "price",
			Type: "uint256",
		},
		{
			Name: "quantity",
			Type: "uint256",
		},
		{
			Name: "isTakerBuy",
			Type: "bool",
		},
		{
			Name: "validUntil",
			Type: "uint64",
		},
		{
			Name: "usd",
			Type: "address",
		},
		{
			Name: "collateralAsset",
			Type: "address",
		},
	},
	"WillRedeem": {
		{Name: "account", Type: "address"},
		{Name: "otoken", Type: "address"},
		{Name: "chainId", Type: "uint256"},
		{Name: "willRedeem", Type: "bool"},
		{Name: "nonce", Type: "string"},
	},
	"Transfer": {
		{
			Name: "user",
			Type: "address",
		},
		{
			Name: "asset",
			Type: "address",
		},
		{
			Name: "chainId",
			Type: "uint256",
		},
		{
			Name: "amount",
			Type: "uint256",
		},
		{
			Name: "isDeposit",
			Type: "bool",
		},
		{
			Name: "nonce",
			Type: "uint64",
		},
	},
}

// confirmationType is the Confirmation struct a taker signs to accept a quote,
// in v12 and premium alike. The field order is part of the typehash, so it has
// to match the contracts' exactly.
var confirmationType = []apitypes.Type{
	{Name: "maker", Type: "address"},
	{Name: "assetAddress", Type: "address"},
	{Name: "chainId", Type: "uint256"},
	{Name: "expiry", Type: "uint64"},
	{Name: "isPut", Type: "bool"},
	{Name: "nonce", Type: "uint64"},
	{Name: "price", Type: "uint256"},
	{Name: "quantity", Type: "uint256"},
	{Name: "quoteNonce", Type: "uint64"},
	{Name: "quoteSignature", Type: "bytes"},
	{Name: "strike", Type: "uint256"},
	{Name: "taker", Type: "address"},
	{Name: "isTakerBuy", Type: "bool"},
	{Name: "usd", Type: "address"},
	{Name: "collateralAsset", Type: "address"},
	{Name: "collateralAmount", Type: "uint256"},
	{Name: "gasFee", Type: "uint256"},
}

func EncodeTypedData(typedData *apitypes.TypedData) (common.Hash, error) {
	hash, _, err := apitypes.TypedDataAndHash(*typedData)
	return common.BytesToHash(hash), err
}

// confirmationHash is the digest a taker signs to accept a quote.
func confirmationHash(domain *apitypes.TypedDataDomain, message map[string]any) ([]byte, error) {
	hash, err := EncodeTypedData(&apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": (*EIP712_TYPES)["EIP712Domain"],
			"Confirmation": confirmationType,
		},
		PrimaryType: "Confirmation",
		Domain:      *domain,
		Message:     message,
	})
	return hash.Bytes(), err
}

// Signs msg with EIP712 signing scheme
func Sign(message []byte, privateKey string) (string, error) {
	privateKeyEcdsa, err := crypto.HexToECDSA(privateKey)
	if err != nil {
		return "", err
	}
	sig, err := crypto.Sign(message, privateKeyEcdsa)
	if err != nil {
		return "", err
	}
	sig[64] += 27
	return fmt.Sprintf("0x%s", common.Bytes2Hex(sig)), nil
}

func createEIP712Domain(chainId int64) *apitypes.TypedDataDomain {
	return &apitypes.TypedDataDomain{
		Name:              "rysk",
		Version:           "0.0.0",
		ChainId:           math.NewHexOrDecimal256(chainId),
		VerifyingContract: ADDRESSES[int(chainId)].Rysk.String(),
	}
}

// TypedDataDomainOverride carries the overrides for the EIP712 domain. It is
// all or nothing: either every field is set and the domain is taken from it, or
// none are and the default domain for the chain is used. The domain's chainId is
// always the chain the message is for, so it is not overridable.
type TypedDataDomainOverride struct {
	Name              string
	Version           string
	VerifyingContract string
}

// CreateTypedDataDomain builds the EIP712 domain for chainId, from override if
// it is set. A partial override is an error: signing against a domain that is
// half default and half caller-supplied is never what the caller meant.
func CreateTypedDataDomain(chainId int64, override TypedDataDomainOverride) (*apitypes.TypedDataDomain, error) {
	set := map[string]bool{
		"name":               override.Name != "",
		"version":            override.Version != "",
		"verifying contract": override.VerifyingContract != "",
	}

	missing := []string{}
	for field, ok := range set {
		if !ok {
			missing = append(missing, field)
		}
	}
	sort.Strings(missing)

	if len(missing) == len(set) {
		return createEIP712Domain(chainId), nil
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("domain is incomplete: also provide the domain %s", strings.Join(missing, " and "))
	}

	if !common.IsHexAddress(override.VerifyingContract) {
		return nil, fmt.Errorf("invalid domain verifying contract %q", override.VerifyingContract)
	}

	return &apitypes.TypedDataDomain{
		Name:              override.Name,
		Version:           override.Version,
		ChainId:           math.NewHexOrDecimal256(chainId),
		VerifyingContract: override.VerifyingContract,
	}, nil
}

func createEIP712TypedData(domain *apitypes.TypedDataDomain, msgType string, msg map[string]interface{}) *apitypes.TypedData {
	return &apitypes.TypedData{
		Types:       *EIP712_TYPES,
		PrimaryType: msgType,
		Domain:      *domain,
		Message:     msg,
	}
}

// CreateQuoteMessage hashes q for signing. A nil domain uses the default domain
// for the quote's chain. premiumAsset is not part of the signed Quote type.
func CreateQuoteMessage(q Quote, domain *apitypes.TypedDataDomain) ([]byte, *apitypes.TypedData, error) {
	if domain == nil {
		domain = createEIP712Domain(int64(q.ChainID))
	}
	return hashMessage(q, domain, "Quote", "signature", "premiumAsset")
}

func CreateTransferMessage(t Transfer) ([]byte, *apitypes.TypedData, error) {
	return hashMessage(t, createEIP712Domain(int64(t.ChainID)), "Transfer", "signature")
}

func CreateWillRedeemMessage(w WillRedeem) ([]byte, error) {
	hash, _, err := hashMessage(w, createEIP712Domain(int64(w.ChainID)), "WillRedeem", "signature")
	return hash, err
}

// hashMessage hashes v, as its json, as a primaryType under domain, leaving out
// the fields in drop that are not part of the signed type.
func hashMessage(v any, domain *apitypes.TypedDataDomain, primaryType string, drop ...string) ([]byte, *apitypes.TypedData, error) {
	msg, _ := json.Marshal(v)
	var message map[string]interface{}
	json.Unmarshal(msg, &message)
	for _, field := range drop {
		delete(message, field)
	}
	typedData := createEIP712TypedData(domain, primaryType, message)
	hash, err := EncodeTypedData(typedData)
	if err != nil {
		return nil, typedData, err
	}
	return hash.Bytes(), typedData, nil
}
