package main

import (
	"math/big"
	"testing"
)

func testQuote() Quote {
	return Quote{
		AssetAddress:    "0x98d56648c9b7f3cb49531f4135115b5000ab1733",
		ChainID:         CHAIN_ID_BASE_SEPOLIA,
		Expiry:          1767225600,
		IsPut:           true,
		IsTakerBuy:      false,
		Maker:           "0x1000000000000000000000000000000000000001",
		Nonce:           "1",
		Price:           "1000000",
		Quantity:        "1000000000000000000",
		Strike:          "2000000000",
		ValidUntil:      1767139200,
		USD:             "0x98d56648c9b7f3cb49531f4135115b5000ab1733",
		CollateralAsset: "0x98d56648c9b7f3cb49531f4135115b5000ab1733",
	}
}

func TestParseTypedDataDomainDefaults(t *testing.T) {
	chainID := int64(CHAIN_ID_BASE_SEPOLIA)
	want := ADDRESSES[CHAIN_ID_BASE_SEPOLIA].Rysk.String()

	for _, raw := range []string{"", "   "} {
		domain, err := ParseTypedDataDomain(chainID, raw)
		if err != nil {
			t.Fatalf("ParseTypedDataDomain(%q): %v", raw, err)
		}
		if domain.Name != "rysk" || domain.Version != "0.0.0" {
			t.Errorf("got name %q version %q, want rysk/0.0.0", domain.Name, domain.Version)
		}
		if domain.VerifyingContract != want {
			t.Errorf("got verifyingContract %q, want %q", domain.VerifyingContract, want)
		}
		if domain.ChainId == nil || (*big.Int)(domain.ChainId).Int64() != chainID {
			t.Errorf("got chainId %v, want %d", domain.ChainId, chainID)
		}
		if domain.Salt != "" {
			t.Errorf("got salt %q, want empty", domain.Salt)
		}
	}
}

func TestParseTypedDataDomainOverrides(t *testing.T) {
	chainID := int64(CHAIN_ID_BASE_SEPOLIA)
	other := "0x2000000000000000000000000000000000000002"

	domain, err := ParseTypedDataDomain(chainID, `{"verifyingContract":"`+other+`"}`)
	if err != nil {
		t.Fatalf("ParseTypedDataDomain: %v", err)
	}
	if domain.VerifyingContract != other {
		t.Errorf("got verifyingContract %q, want %q", domain.VerifyingContract, other)
	}
	// untouched fields keep their defaults
	if domain.Name != "rysk" || domain.Version != "0.0.0" || domain.ChainId == nil {
		t.Errorf("override clobbered defaults: %+v", domain)
	}
}

func TestParseTypedDataDomainNullKeepsDefault(t *testing.T) {
	domain, err := ParseTypedDataDomain(int64(CHAIN_ID_BASE_SEPOLIA), `{"chainId":null,"name":null}`)
	if err != nil {
		t.Fatalf("ParseTypedDataDomain: %v", err)
	}
	if domain.ChainId == nil || (*big.Int)(domain.ChainId).Int64() != int64(CHAIN_ID_BASE_SEPOLIA) {
		t.Errorf("null chainId did not keep the default: %v", domain.ChainId)
	}
	if domain.Name != "rysk" {
		t.Errorf("null name did not keep the default: %q", domain.Name)
	}
}

func TestParseTypedDataDomainChainIdFormats(t *testing.T) {
	for _, raw := range []string{`{"chainId":8453}`, `{"chainId":"8453"}`, `{"chainId":"0x2105"}`} {
		domain, err := ParseTypedDataDomain(int64(CHAIN_ID_BASE_SEPOLIA), raw)
		if err != nil {
			t.Fatalf("ParseTypedDataDomain(%s): %v", raw, err)
		}
		if got, want := (*big.Int)(domain.ChainId).Int64(), int64(CHAIN_ID_BASE); got != want {
			t.Errorf("%s: got chainId %d, want %d", raw, got, want)
		}
	}
}

func TestParseTypedDataDomainErrors(t *testing.T) {
	cases := map[string]string{
		"malformed json":             `{"name":`,
		"bad verifyingContract":      `{"verifyingContract":"0xnothex"}`,
		"empty name":                 `{"name":""}`,
		"empty version":              `{"version":""}`,
		"empty verifyingContract":    `{"verifyingContract":""}`,
		"salt is not a domain field": `{"salt":"0x0000000000000000000000000000000000000000000000000000000000000001"}`,
		"unknown field":              `{"nope":1}`,
	}
	for name, raw := range cases {
		if _, err := ParseTypedDataDomain(int64(CHAIN_ID_BASE_SEPOLIA), raw); err == nil {
			t.Errorf("%s: expected an error for %s", name, raw)
		}
	}
}

func TestCreateQuoteMessageDomainAffectsHash(t *testing.T) {
	q := testQuote()

	defaultHash, defaultData, err := CreateQuoteMessage(q, nil)
	if err != nil {
		t.Fatalf("default domain: %v", err)
	}
	if got := len(defaultData.Types["EIP712Domain"]); got != 4 {
		t.Errorf("got %d default domain fields, want 4", got)
	}

	// An explicit domain equal to the default must not change the signed hash.
	explicit, err := ParseTypedDataDomain(int64(q.ChainID), `{"name":"rysk"}`)
	if err != nil {
		t.Fatalf("explicit domain: %v", err)
	}
	explicitHash, _, err := CreateQuoteMessage(q, explicit)
	if err != nil {
		t.Fatalf("explicit domain: %v", err)
	}
	if string(explicitHash) != string(defaultHash) {
		t.Error("explicit default-equivalent domain changed the hash")
	}

	// A different verifyingContract must change it.
	overridden, err := ParseTypedDataDomain(int64(q.ChainID), `{"verifyingContract":"0x2000000000000000000000000000000000000002"}`)
	if err != nil {
		t.Fatalf("overridden domain: %v", err)
	}
	overriddenHash, _, err := CreateQuoteMessage(q, overridden)
	if err != nil {
		t.Fatalf("overridden domain: %v", err)
	}
	if string(overriddenHash) == string(defaultHash) {
		t.Error("verifyingContract override did not change the hash")
	}
}

func TestCreateQuoteMessageDomainFieldsAreFixed(t *testing.T) {
	q := testQuote()

	domain, err := ParseTypedDataDomain(int64(q.ChainID), `{"name":"custom","version":"1"}`)
	if err != nil {
		t.Fatalf("custom domain: %v", err)
	}
	customHash, customData, err := CreateQuoteMessage(q, domain)
	if err != nil {
		t.Fatalf("custom domain: %v", err)
	}

	want := []string{"name", "version", "chainId", "verifyingContract"}
	fields := customData.Types["EIP712Domain"]
	if len(fields) != len(want) {
		t.Fatalf("got %d domain fields, want %d", len(fields), len(want))
	}
	for i, name := range want {
		if fields[i].Name != name {
			t.Errorf("domain field %d is %q, want %q", i, fields[i].Name, name)
		}
	}

	defaultHash, _, err := CreateQuoteMessage(q, nil)
	if err != nil {
		t.Fatalf("default domain: %v", err)
	}
	if string(customHash) == string(defaultHash) {
		t.Error("custom name/version did not change the hash")
	}
}

func TestCreateTransferMessageUnaffected(t *testing.T) {
	tr := Transfer{
		User:      "0x1000000000000000000000000000000000000001",
		Asset:     "0x98d56648c9b7f3cb49531f4135115b5000ab1733",
		ChainID:   CHAIN_ID_BASE_SEPOLIA,
		Amount:    "1000000",
		IsDeposit: true,
		Nonce:     "1",
	}
	_, typedData, err := CreateTransferMessage(tr)
	if err != nil {
		t.Fatalf("CreateTransferMessage: %v", err)
	}
	if got := len(typedData.Types["EIP712Domain"]); got != 4 {
		t.Errorf("got %d transfer domain fields, want 4", got)
	}
}
