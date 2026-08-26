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

func TestCreateTypedDataDomainDefaults(t *testing.T) {
	chainID := int64(CHAIN_ID_BASE_SEPOLIA)
	want := ADDRESSES[CHAIN_ID_BASE_SEPOLIA].Rysk.String()

	domain, err := CreateTypedDataDomain(chainID, TypedDataDomainOverride{})
	if err != nil {
		t.Fatalf("CreateTypedDataDomain: %v", err)
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

func TestCreateTypedDataDomainOverridesOneField(t *testing.T) {
	chainID := int64(CHAIN_ID_BASE_SEPOLIA)
	other := "0x2000000000000000000000000000000000000002"

	domain, err := CreateTypedDataDomain(chainID, TypedDataDomainOverride{VerifyingContract: other})
	if err != nil {
		t.Fatalf("CreateTypedDataDomain: %v", err)
	}
	if domain.VerifyingContract != other {
		t.Errorf("got verifyingContract %q, want %q", domain.VerifyingContract, other)
	}
	// untouched fields keep their defaults
	if domain.Name != "rysk" || domain.Version != "0.0.0" || domain.ChainId == nil {
		t.Errorf("override clobbered defaults: %+v", domain)
	}
	if (*big.Int)(domain.ChainId).Int64() != chainID {
		t.Errorf("got chainId %v, want %d", domain.ChainId, chainID)
	}
}

func TestCreateTypedDataDomainErrors(t *testing.T) {
	cases := map[string]TypedDataDomainOverride{
		"bad verifyingContract": {VerifyingContract: "0xnothex"},
	}
	for name, override := range cases {
		if _, err := CreateTypedDataDomain(int64(CHAIN_ID_BASE_SEPOLIA), override); err == nil {
			t.Errorf("%s: expected an error for %+v", name, override)
		}
	}
}

func TestCreateTypedDataDomainUnknownChain(t *testing.T) {
	// A chain absent from ADDRESSES has no Rysk contract, so the default domain
	// carries the zero address - which is what the domain flags are for.
	domain, err := CreateTypedDataDomain(1234, TypedDataDomainOverride{})
	if err != nil {
		t.Fatalf("CreateTypedDataDomain: %v", err)
	}
	if domain.VerifyingContract != ZeroAddress.String() {
		t.Errorf("got verifyingContract %q, want %q", domain.VerifyingContract, ZeroAddress.String())
	}

	other := "0x2000000000000000000000000000000000000002"
	domain, err = CreateTypedDataDomain(1234, TypedDataDomainOverride{VerifyingContract: other})
	if err != nil {
		t.Fatalf("CreateTypedDataDomain: %v", err)
	}
	if domain.VerifyingContract != other {
		t.Errorf("got verifyingContract %q, want %q", domain.VerifyingContract, other)
	}
}

func TestCreateQuoteMessageDomainAffectsHash(t *testing.T) {
	q := testQuote()

	defaultHash, defaultData, err := CreateQuoteMessage(q, nil)
	if err != nil {
		t.Fatalf("default domain: %v", err)
	}

	want := []string{"name", "version", "chainId", "verifyingContract"}
	fields := defaultData.Types["EIP712Domain"]
	if len(fields) != len(want) {
		t.Fatalf("got %d domain fields, want %d", len(fields), len(want))
	}
	for i, name := range want {
		if fields[i].Name != name {
			t.Errorf("domain field %d is %q, want %q", i, fields[i].Name, name)
		}
	}

	// An explicit domain equal to the default must not change the signed hash.
	explicit, err := CreateTypedDataDomain(int64(q.ChainID), TypedDataDomainOverride{Name: "rysk", Version: "0.0.0"})
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

	// Each overridable field must change it.
	overrides := map[string]TypedDataDomainOverride{
		"name":              {Name: "custom"},
		"version":           {Version: "1"},
		"verifyingContract": {VerifyingContract: "0x2000000000000000000000000000000000000002"},
	}
	for field, override := range overrides {
		domain, err := CreateTypedDataDomain(int64(q.ChainID), override)
		if err != nil {
			t.Fatalf("%s override: %v", field, err)
		}
		hash, _, err := CreateQuoteMessage(q, domain)
		if err != nil {
			t.Fatalf("%s override: %v", field, err)
		}
		if string(hash) == string(defaultHash) {
			t.Errorf("%s override did not change the hash", field)
		}
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
