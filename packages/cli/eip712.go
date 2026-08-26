package main

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	crypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"github.com/goccy/go-json"
)

var ZeroAddress = common.HexToAddress("0x0")

// EIP712_DOMAIN_FIELDS lists every supported domain field with its type, in the
// order EIP712 defines them. Only the fields a domain actually populates end up
// in its EIP712Domain type, as the spec requires.
var EIP712_DOMAIN_FIELDS = []apitypes.Type{
	{Name: "name", Type: "string"},
	{Name: "version", Type: "string"},
	{Name: "chainId", Type: "uint256"},
	{Name: "verifyingContract", Type: "address"},
	{Name: "salt", Type: "bytes32"},
}

var EIP712_TYPES = &apitypes.Types{
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

// EncodeTypedData - Encoding the typed data
func EncodeTypedData(typedData *apitypes.TypedData) (common.Hash, error) {
	domainSeparator, err := typedData.HashStruct("EIP712Domain", typedData.Domain.Map())
	if err != nil {
		return common.BytesToHash([]byte{}), err
	}
	typedDataHash, err := typedData.HashStruct(typedData.PrimaryType, typedData.Message)
	if err != nil {
		return common.BytesToHash([]byte{}), err
	}
	rawData := fmt.Appendf(nil, "\x19\x01%s%s", string(domainSeparator), string(typedDataHash))
	hash := common.BytesToHash(crypto.Keccak256(rawData))
	return hash, err
}

// Signs msg with EIP712 signing scheme
func Sign(message []byte, privateKey string) (string, error) {
	privateKeyEcdsa, err := crypto.HexToECDSA(privateKey)
	if err != nil {
		return "", err
	}
	sigBytes, err := signTypedData(message, privateKeyEcdsa)
	if err != nil {
		return "", err
	}
	signature := fmt.Sprintf("0x%s", common.Bytes2Hex(sigBytes))
	return signature, nil
}

func signTypedData(message []byte, privateKey *ecdsa.PrivateKey) (sig []byte, err error) {
	sig, err = crypto.Sign(message, privateKey)
	if err != nil {
		return sig, err
	}
	sig[64] += 27
	return
}

func createEIP712Domain(chainId int64) *apitypes.TypedDataDomain {
	return &apitypes.TypedDataDomain{
		Name:              "rysk",
		Version:           "0.0.0",
		ChainId:           math.NewHexOrDecimal256(chainId),
		VerifyingContract: ADDRESSES[int(chainId)].Rysk.String(),
	}
}

// ParseTypedDataDomain layers a JSON EIP712 domain over the default domain for
// chainId. Fields the JSON omits keep their default value, so a caller can
// override just the verifyingContract; setting a field to "" (or chainId to
// null) drops it from the domain entirely. An empty json string returns the
// default domain unchanged.
func ParseTypedDataDomain(chainId int64, raw string) (*apitypes.TypedDataDomain, error) {
	domain := *createEIP712Domain(chainId)

	raw = strings.TrimSpace(raw)
	if raw == "" {
		return &domain, nil
	}

	if err := json.Unmarshal([]byte(raw), &domain); err != nil {
		return nil, fmt.Errorf("invalid domain json: %w", err)
	}

	if domain.VerifyingContract != "" && !common.IsHexAddress(domain.VerifyingContract) {
		return nil, fmt.Errorf("invalid domain verifyingContract %q", domain.VerifyingContract)
	}
	if len(domain.Map()) == 0 {
		return nil, errors.New("domain is undefined: every field was dropped")
	}

	return &domain, nil
}

// domainType returns the EIP712Domain type covering exactly the fields the
// domain populates. Hashing a domain against a type that names absent fields
// fails, and one that omits present fields silently ignores them.
func domainType(domain *apitypes.TypedDataDomain) []apitypes.Type {
	populated := domain.Map()
	fields := make([]apitypes.Type, 0, len(EIP712_DOMAIN_FIELDS))
	for _, field := range EIP712_DOMAIN_FIELDS {
		if _, ok := populated[field.Name]; ok {
			fields = append(fields, field)
		}
	}
	return fields
}

func createEIP712TypedData(domain *apitypes.TypedDataDomain, msgType string, msg map[string]interface{}) *apitypes.TypedData {
	types := apitypes.Types{"EIP712Domain": domainType(domain)}
	for name, fields := range *EIP712_TYPES {
		types[name] = fields
	}
	return &apitypes.TypedData{
		Types:       types,
		PrimaryType: msgType,
		Domain:      *domain,
		Message:     msg,
	}
}

// CreateQuoteMessage hashes q for signing. A nil domain uses the default domain
// for the quote's chain.
func CreateQuoteMessage(q Quote, domain *apitypes.TypedDataDomain) (messageHash []byte, typedData *apitypes.TypedData, err error) {
	msg, _ := json.Marshal(q)
	var imessage map[string]interface{}
	json.Unmarshal(msg, &imessage)
	// remove extra fields
	delete(imessage, "signature")
	if domain == nil {
		domain = createEIP712Domain(int64(q.ChainID))
	}
	typedData = createEIP712TypedData(domain, "Quote", imessage)
	hash, err := EncodeTypedData(typedData)
	if err != nil {
		return nil, typedData, err
	}
	messageHash = hash.Bytes()
	return messageHash, typedData, nil
}

func CreateTransferMessage(t Transfer) (messageHash []byte, typedData *apitypes.TypedData, err error) {
	msg, _ := json.Marshal(t)
	var imessage map[string]interface{}
	json.Unmarshal(msg, &imessage)
	// remove extra fields
	delete(imessage, "signature")
	typedData = createEIP712TypedData(createEIP712Domain(int64(t.ChainID)), "Transfer", imessage)
	hash, err := EncodeTypedData(typedData)
	if err != nil {
		return nil, typedData, err
	}
	messageHash = hash.Bytes()
	return messageHash, typedData, nil
}
