package main

import (
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	crypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"github.com/goccy/go-json"
	"github.com/urfave/cli/v2"
)

const testPrivateKey = "4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"

const testOptionHandler = "0x2000000000000000000000000000000000000002"

func testKeyAddress(t *testing.T) common.Address {
	t.Helper()
	key, err := crypto.HexToECDSA(testPrivateKey)
	if err != nil {
		t.Fatalf("HexToECDSA: %v", err)
	}
	return crypto.PubkeyToAddress(key.PublicKey)
}

// recoverSigner undoes what Sign does: the signature carries v as 27/28.
func recoverSigner(t *testing.T, hash []byte, signature string) common.Address {
	t.Helper()
	sig := common.FromHex(signature)
	if len(sig) != 65 {
		t.Fatalf("signature is %d bytes, want 65", len(sig))
	}
	if sig[64] != 27 && sig[64] != 28 {
		t.Fatalf("signature v is %d, want 27 or 28", sig[64])
	}
	sig[64] -= 27

	pub, err := crypto.SigToPub(hash, sig)
	if err != nil {
		t.Fatalf("SigToPub: %v", err)
	}
	return crypto.PubkeyToAddress(*pub)
}

func testPremiumQuoteInput(t *testing.T) premiumQuoteInput {
	t.Helper()
	return premiumQuoteInput{
		Quote: Quote{
			AssetAddress:    "0x98d56648c9b7f3cb49531f4135115b5000ab1733",
			ChainID:         CHAIN_ID_BASE_SEPOLIA,
			Expiry:          1767225600,
			IsPut:           true,
			IsTakerBuy:      false,
			Maker:           testKeyAddress(t).String(),
			Nonce:           "42",
			Price:           "1250000000000000000",
			Quantity:        "1000000000000000000",
			Strike:          "300000000000",
			ValidUntil:      time.Now().Add(5 * time.Minute).Unix(),
			USD:             "0x98d56648c9b7f3cb49531f4135115b5000ab1733",
			CollateralAsset: "0x98d56648c9b7f3cb49531f4135115b5000ab1733",
		},
		RequestID: "b7c2-uuid",
		Domain:    &premiumDomainInput{VerifyingContract: testOptionHandler},
	}
}

// runPremium drives the command the way the binary does, flags and all.
func runPremium(args ...string) error {
	app := &cli.App{
		Name:      "ryskV12",
		Commands:  []*cli.Command{premiumAction},
		Writer:    io.Discard,
		ErrWriter: io.Discard,
	}
	return app.Run(append([]string{"ryskV12", "premium"}, args...))
}

// captureStdout collects what a command prints, since the api's response is the
// command's real output.
func captureStdout(t *testing.T, run func() error) (string, error) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	original := os.Stdout
	os.Stdout = writer

	runErr := run()

	os.Stdout = original
	writer.Close()
	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	return string(out), runErr
}

func TestPremiumQuoteDomainDefaults(t *testing.T) {
	domain, err := premiumQuoteDomain(CHAIN_ID_BASE_SEPOLIA, &premiumDomainInput{VerifyingContract: testOptionHandler})
	if err != nil {
		t.Fatalf("premiumQuoteDomain: %v", err)
	}
	if domain.Name != PREMIUM_DOMAIN_NAME || domain.Version != PREMIUM_DOMAIN_VERSION {
		t.Errorf("got %q/%q, want %q/%q", domain.Name, domain.Version, PREMIUM_DOMAIN_NAME, PREMIUM_DOMAIN_VERSION)
	}
	if domain.VerifyingContract != testOptionHandler {
		t.Errorf("got verifyingContract %q, want %q", domain.VerifyingContract, testOptionHandler)
	}
	if domain.ChainId == nil || (*big.Int)(domain.ChainId).Int64() != int64(CHAIN_ID_BASE_SEPOLIA) {
		t.Errorf("got chainId %v, want %d", domain.ChainId, CHAIN_ID_BASE_SEPOLIA)
	}
}

func TestPremiumQuoteDomainErrors(t *testing.T) {
	cases := map[string]*premiumDomainInput{
		"no verifying contract":  {},
		"bad verifying contract": {VerifyingContract: "0xnothex"},
		"salt":                   {VerifyingContract: testOptionHandler, Salt: "0x01"},
		"nil":                    nil,
	}
	for name, input := range cases {
		if _, err := premiumQuoteDomain(CHAIN_ID_BASE_SEPOLIA, input); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}

	other := premiumDomainInput{VerifyingContract: testOptionHandler}
	if err := json.Unmarshal([]byte(`{"chainId":8453}`), &other); err != nil {
		t.Fatalf("unmarshal chainId: %v", err)
	}
	if _, err := premiumQuoteDomain(CHAIN_ID_BASE_SEPOLIA, &other); err == nil {
		t.Error("expected an error for a domain on another chain")
	}
}

func TestPremiumPrepareQuoteSignsUnderTheHandlerDomain(t *testing.T) {
	input := testPremiumQuoteInput(t)

	payload, err := premiumPrepareQuote(input, testPrivateKey, time.Now())
	if err != nil {
		t.Fatalf("premiumPrepareQuote: %v", err)
	}

	if payload.RequestID != input.RequestID || payload.Price != input.Price ||
		payload.Nonce != input.Nonce || payload.ValidUntil != input.ValidUntil {
		t.Errorf("transport fields not carried through: %+v", payload)
	}
	if payload.Maker != strings.ToLower(input.Maker) {
		t.Errorf("got maker %q, want it lowercased", payload.Maker)
	}

	domain, err := premiumQuoteDomain(input.ChainID, input.Domain)
	if err != nil {
		t.Fatalf("premiumQuoteDomain: %v", err)
	}
	hash, _, err := CreateQuoteMessage(input.Quote, domain)
	if err != nil {
		t.Fatalf("CreateQuoteMessage: %v", err)
	}
	if signer := recoverSigner(t, hash, payload.Signature); signer != testKeyAddress(t) {
		t.Errorf("signature recovers to %s, want %s", signer, testKeyAddress(t))
	}

	// The websocket flow's default domain must not verify the same signature.
	v12Domain, err := CreateTypedDataDomain(int64(input.ChainID), TypedDataDomainOverride{})
	if err != nil {
		t.Fatalf("CreateTypedDataDomain: %v", err)
	}
	v12Hash, _, err := CreateQuoteMessage(input.Quote, v12Domain)
	if err != nil {
		t.Fatalf("CreateQuoteMessage: %v", err)
	}
	if signer := recoverSigner(t, v12Hash, payload.Signature); signer == testKeyAddress(t) {
		t.Error("the quote also verifies under the v12 rysk domain")
	}
}

func TestPremiumPrepareQuoteValidation(t *testing.T) {
	now := time.Now()
	cases := map[string]func(*premiumQuoteInput){
		"no request id":           func(q *premiumQuoteInput) { q.RequestID = "" },
		"bad maker":               func(q *premiumQuoteInput) { q.Maker = "0xnothex" },
		"bad asset":               func(q *premiumQuoteInput) { q.AssetAddress = "nope" },
		"bad usd":                 func(q *premiumQuoteInput) { q.USD = "" },
		"bad collateral":          func(q *premiumQuoteInput) { q.CollateralAsset = "" },
		"no chain id":             func(q *premiumQuoteInput) { q.ChainID = 0 },
		"bad nonce":               func(q *premiumQuoteInput) { q.Nonce = "one" },
		"negative nonce":          func(q *premiumQuoteInput) { q.Nonce = "-1" },
		"no price":                func(q *premiumQuoteInput) { q.Price = "" },
		"zero price":              func(q *premiumQuoteInput) { q.Price = "0" },
		"valid until in ms":       func(q *premiumQuoteInput) { q.ValidUntil = now.Add(5 * time.Minute).UnixMilli() },
		"valid until too close":   func(q *premiumQuoteInput) { q.ValidUntil = now.Add(time.Minute).Unix() },
		"valid until too far":     func(q *premiumQuoteInput) { q.ValidUntil = now.Add(11 * time.Minute).Unix() },
		"no verifying contract":   func(q *premiumQuoteInput) { q.Domain = &premiumDomainInput{} },
		"valid until in the past": func(q *premiumQuoteInput) { q.ValidUntil = now.Add(-time.Minute).Unix() },
	}
	for name, mutate := range cases {
		input := testPremiumQuoteInput(t)
		mutate(&input)
		if _, err := premiumPrepareQuote(input, testPrivateKey, now); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestPremiumAuthHeaders(t *testing.T) {
	headers, err := premiumAuthHeaders(int64(CHAIN_ID_BASE_SEPOLIA), "42", testPrivateKey)
	if err != nil {
		t.Fatalf("premiumAuthHeaders: %v", err)
	}

	if got := headers["X-Chain-Id"]; got != strconv.Itoa(CHAIN_ID_BASE_SEPOLIA) {
		t.Errorf("got X-Chain-Id %q, want %d", got, CHAIN_ID_BASE_SEPOLIA)
	}
	if got := headers["X-Nonce"]; got != "42" {
		t.Errorf("got X-Nonce %q, want 42", got)
	}

	// Rebuild the digest the api recovers from, and check it is ours.
	hash, err := EncodeTypedData(premiumAuthTypedData(int64(CHAIN_ID_BASE_SEPOLIA), "42"))
	if err != nil {
		t.Fatalf("EncodeTypedData: %v", err)
	}
	if signer := recoverSigner(t, hash.Bytes(), headers["X-Signature"]); signer != testKeyAddress(t) {
		t.Errorf("auth signature recovers to %s, want %s", signer, testKeyAddress(t))
	}

	// A quote over the same nonce must produce a different signature, or an auth
	// header could be replayed as a trade.
	input := testPremiumQuoteInput(t)
	payload, err := premiumPrepareQuote(input, testPrivateKey, time.Now())
	if err != nil {
		t.Fatalf("premiumPrepareQuote: %v", err)
	}
	if payload.Signature == headers["X-Signature"] {
		t.Error("auth and quote signatures are identical")
	}
}

func TestPremiumStatusError(t *testing.T) {
	const base = "https://premium.rysk.finance"

	if err := premiumStatusError(base, http.StatusOK, []byte(`{"failures":[]}`)); err != nil {
		t.Errorf("200: %v", err)
	}
	if err := premiumStatusError(base, http.StatusNoContent, nil); err != nil {
		t.Errorf("204: %v", err)
	}

	err := premiumStatusError(base, http.StatusTooManyRequests, []byte("cc"))
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("429 gave %v, want a rate limit error", err)
	}

	err = premiumStatusError(base, http.StatusBadRequest, []byte("request expired"))
	if err == nil || !strings.Contains(err.Error(), "request expired") {
		t.Errorf("400 gave %v, want the body verbatim", err)
	}

	// a host without the rfq routes is the common case for a wrong --url, so the
	// error has to say so rather than read like a missing id
	err = premiumStatusError(base, http.StatusNotFound, []byte("404 page not found"))
	if err == nil || !strings.Contains(err.Error(), "no rfq routes") || !strings.Contains(err.Error(), base) {
		t.Errorf("404 gave %v, want it to name the base url and the missing routes", err)
	}

	// a 404 the api itself wrote is passed through
	err = premiumStatusError(base, http.StatusNotFound, []byte("quote not found"))
	if err == nil || !strings.Contains(err.Error(), "quote not found") {
		t.Errorf("404 gave %v, want the body verbatim", err)
	}
}

func TestPremiumPrivateKeyFromEnv(t *testing.T) {
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{"failures":[]}`))
	}))
	defer server.Close()

	inputs := []premiumQuoteInput{testPremiumQuoteInput(t)}
	batch, err := json.Marshal(inputs)
	if err != nil {
		t.Fatalf("marshal batch: %v", err)
	}
	file := t.TempDir() + "/batch.json"
	if err := os.WriteFile(file, batch, 0o600); err != nil {
		t.Fatalf("write batch: %v", err)
	}

	// the key never has to appear in argv, where ps would show it
	t.Setenv("RYSK_PRIVATE_KEY", testPrivateKey)
	if _, err := captureStdout(t, func() error {
		return runPremium("quote", "--url", server.URL, "--batch", file)
	}); err != nil {
		t.Fatalf("premium quote with the key in the environment: %v", err)
	}

	var posted []premiumQuotePayload
	if err := json.Unmarshal(body, &posted); err != nil {
		t.Fatalf("body is not a json array: %v (%s)", err, body)
	}
	if len(posted) != 1 || posted[0].Signature == "" {
		t.Fatalf("got %d quotes, want 1 signed one", len(posted))
	}
	hash, _, err := CreateQuoteMessage(inputs[0].Quote, mustPremiumDomain(t, inputs[0]))
	if err != nil {
		t.Fatalf("CreateQuoteMessage: %v", err)
	}
	if signer := recoverSigner(t, hash, posted[0].Signature); signer != testKeyAddress(t) {
		t.Errorf("signed by %s, want %s", signer, testKeyAddress(t))
	}
}

func mustPremiumDomain(t *testing.T, input premiumQuoteInput) *apitypes.TypedDataDomain {
	t.Helper()
	domain, err := premiumQuoteDomain(input.ChainID, input.Domain)
	if err != nil {
		t.Fatalf("premiumQuoteDomain: %v", err)
	}
	return domain
}

func TestPremiumQuoteResult(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return premiumQuoteResult([]byte(`{"failures":[]}`), 1)
	})
	if err != nil {
		t.Errorf("empty failures gave %v, want nil", err)
	}
	if !strings.Contains(out, `{"failures":[]}`) {
		t.Errorf("response not printed: %q", out)
	}

	out, err = captureStdout(t, func() error {
		return premiumQuoteResult([]byte(`{"failures":[{"error":"request expired"}]}`), 3)
	})
	if err == nil || !strings.Contains(err.Error(), "1 of 3 quotes rejected") {
		t.Errorf("failures gave %v, want a rejection error", err)
	}
	if !strings.Contains(out, "request expired") {
		t.Errorf("response not printed on failure: %q", out)
	}

	if _, err := captureStdout(t, func() error { return premiumQuoteResult(nil, 1) }); err == nil {
		t.Error("an empty body has to be treated as a failure")
	}
}

func TestPremiumQuotePostsOnlyTransportFields(t *testing.T) {
	var body []byte
	var method, path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		body, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{"failures":[]}`))
	}))
	defer server.Close()

	input := testPremiumQuoteInput(t)
	_, err := captureStdout(t, func() error {
		return runPremium(
			"quote",
			"--url", server.URL,
			"--request_id", input.RequestID,
			"--asset", input.AssetAddress,
			"--chain_id", strconv.Itoa(input.ChainID),
			"--expiry", strconv.FormatInt(input.Expiry, 10),
			"--is_put",
			"--strike", input.Strike,
			"--quantity", input.Quantity,
			"--usd", input.USD,
			"--collateral", input.CollateralAsset,
			"--maker", input.Maker,
			"--nonce", input.Nonce,
			"--price", input.Price,
			"--valid_until", strconv.FormatInt(input.ValidUntil, 10),
			"--private_key", testPrivateKey,
			"--domain_verifying_contract", testOptionHandler,
		)
	})
	if err != nil {
		t.Fatalf("premium quote: %v", err)
	}

	if method != http.MethodPost || path != "/api/quotes" {
		t.Errorf("got %s %s, want POST /api/quotes", method, path)
	}

	var posted []map[string]any
	if err := json.Unmarshal(body, &posted); err != nil {
		t.Fatalf("body is not a json array: %v (%s)", err, body)
	}
	if len(posted) != 1 {
		t.Fatalf("posted %d quotes, want 1", len(posted))
	}

	want := map[string]bool{"requestId": true, "maker": true, "price": true, "nonce": true, "validUntil": true, "signature": true}
	for field := range posted[0] {
		if !want[field] {
			t.Errorf("quote carries %q, which the api rebuilds from the request", field)
		}
	}
	for field := range want {
		if _, ok := posted[0][field]; !ok {
			t.Errorf("quote is missing %q", field)
		}
	}

	domain, err := premiumQuoteDomain(input.ChainID, input.Domain)
	if err != nil {
		t.Fatalf("premiumQuoteDomain: %v", err)
	}
	hash, _, err := CreateQuoteMessage(input.Quote, domain)
	if err != nil {
		t.Fatalf("CreateQuoteMessage: %v", err)
	}
	if signer := recoverSigner(t, hash, posted[0]["signature"].(string)); signer != testKeyAddress(t) {
		t.Errorf("posted signature recovers to %s, want %s", signer, testKeyAddress(t))
	}
}

func TestPremiumQuoteBatchPostsOneRequest(t *testing.T) {
	calls := 0
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{"failures":[]}`))
	}))
	defer server.Close()

	inputs := make([]premiumQuoteInput, 0, 8)
	for i := 0; i < 8; i++ {
		input := testPremiumQuoteInput(t)
		input.Nonce = strconv.Itoa(100 + i)
		input.RequestID = "request-" + strconv.Itoa(i)
		inputs = append(inputs, input)
	}
	batch, err := json.Marshal(inputs)
	if err != nil {
		t.Fatalf("marshal batch: %v", err)
	}

	file := t.TempDir() + "/batch.json"
	if err := os.WriteFile(file, batch, 0o600); err != nil {
		t.Fatalf("write batch: %v", err)
	}

	if _, err := captureStdout(t, func() error {
		return runPremium("quote", "--url", server.URL, "--batch", file, "--private_key", testPrivateKey)
	}); err != nil {
		t.Fatalf("premium quote --batch: %v", err)
	}

	if calls != 1 {
		t.Errorf("made %d requests, want 1", calls)
	}
	var posted []premiumQuotePayload
	if err := json.Unmarshal(body, &posted); err != nil {
		t.Fatalf("body is not a json array: %v (%s)", err, body)
	}
	if len(posted) != 8 {
		t.Fatalf("posted %d quotes, want 8", len(posted))
	}
	for i, payload := range posted {
		if payload.RequestID != inputs[i].RequestID || payload.Nonce != inputs[i].Nonce {
			t.Errorf("quote %d: got %s/%s, want %s/%s", i, payload.RequestID, payload.Nonce, inputs[i].RequestID, inputs[i].Nonce)
		}
		if payload.Signature == "" {
			t.Errorf("quote %d is unsigned", i)
		}
	}
}

func TestPremiumQuoteRejectsBeforeAnyCall(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"failures":[]}`))
	}))
	defer server.Close()

	input := testPremiumQuoteInput(t)
	base := func() []string {
		return []string{
			"quote",
			"--url", server.URL,
			"--request_id", input.RequestID,
			"--asset", input.AssetAddress,
			"--chain_id", strconv.Itoa(input.ChainID),
			"--expiry", strconv.FormatInt(input.Expiry, 10),
			"--strike", input.Strike,
			"--quantity", input.Quantity,
			"--usd", input.USD,
			"--collateral", input.CollateralAsset,
			"--maker", input.Maker,
			"--price", input.Price,
			"--private_key", testPrivateKey,
		}
	}

	cases := map[string][]string{
		"valid until in ms": {
			"--nonce", "1",
			"--valid_until", strconv.FormatInt(time.Now().Add(5*time.Minute).UnixMilli(), 10),
			"--domain_verifying_contract", testOptionHandler,
		},
		"valid until out of window": {
			"--nonce", "2",
			"--valid_until", strconv.FormatInt(time.Now().Add(30*time.Minute).Unix(), 10),
			"--domain_verifying_contract", testOptionHandler,
		},
		"bad nonce": {
			"--nonce", "not-a-number",
			"--valid_until", strconv.FormatInt(input.ValidUntil, 10),
			"--domain_verifying_contract", testOptionHandler,
		},
		"no verifying contract": {
			"--nonce", "3",
			"--valid_until", strconv.FormatInt(input.ValidUntil, 10),
		},
	}
	for name, extra := range cases {
		args := append(base(), extra...)
		if _, err := captureStdout(t, func() error { return runPremium(args...) }); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}

	if calls != 0 {
		t.Errorf("made %d requests, want none: validation runs before signing and posting", calls)
	}
}

func TestPremiumQuoteBatchIsExclusive(t *testing.T) {
	file := t.TempDir() + "/batch.json"
	if err := os.WriteFile(file, []byte("[]"), 0o600); err != nil {
		t.Fatalf("write batch: %v", err)
	}

	err := runPremium("quote", "--batch", file, "--private_key", testPrivateKey, "--nonce", "1")
	if err == nil || !strings.Contains(err.Error(), "--batch cannot be combined with --nonce") {
		t.Errorf("got %v, want a mutual exclusion error", err)
	}

	if err := runPremium("quote", "--batch", file, "--private_key", testPrivateKey); err == nil {
		t.Error("an empty batch has to be an error")
	}
}

func TestPremiumReadsProxyTheApi(t *testing.T) {
	var path, query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, query = r.URL.Path, r.URL.RawQuery
		w.Write([]byte(`[{"id":"b7c2"}]`))
	}))
	defer server.Close()

	maker := "0x1000000000000000000000000000000000000001"
	cases := []struct {
		args      []string
		wantPath  string
		wantQuery string
	}{
		{[]string{"requests", "--maker", strings.ToUpper(maker)}, "/api/requests/maker", "address=" + maker},
		{[]string{"quotes", "--maker", maker}, "/api/quotes", "address=" + maker},
		{[]string{"quote-status", "--id", "9f31"}, "/api/quotes/9f31", ""},
	}
	for _, tc := range cases {
		out, err := captureStdout(t, func() error {
			return runPremium(append(tc.args, "--url", server.URL)...)
		})
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if path != tc.wantPath || query != tc.wantQuery {
			t.Errorf("%v hit %s?%s, want %s?%s", tc.args, path, query, tc.wantPath, tc.wantQuery)
		}
		if !strings.Contains(out, `[{"id":"b7c2"}]`) {
			t.Errorf("%v printed %q, want the api's body verbatim", tc.args, out)
		}
	}
}

func TestPremiumCancelSendsAuthHeaders(t *testing.T) {
	var method, path string
	var headers http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, headers = r.Method, r.URL.Path, r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	err := runPremium(
		"cancel",
		"--url", server.URL,
		"--id", "9f31",
		"--chain_id", strconv.Itoa(CHAIN_ID_BASE_SEPOLIA),
		"--nonce", "43",
		"--private_key", testPrivateKey,
	)
	if err != nil {
		t.Fatalf("premium cancel: %v", err)
	}

	if method != http.MethodDelete || path != "/api/quotes/9f31" {
		t.Errorf("got %s %s, want DELETE /api/quotes/9f31", method, path)
	}
	if got := headers.Get("X-Nonce"); got != "43" {
		t.Errorf("got X-Nonce %q, want 43", got)
	}
	if got := headers.Get("X-Chain-Id"); got != strconv.Itoa(CHAIN_ID_BASE_SEPOLIA) {
		t.Errorf("got X-Chain-Id %q, want %d", got, CHAIN_ID_BASE_SEPOLIA)
	}

	hash, err := EncodeTypedData(premiumAuthTypedData(int64(CHAIN_ID_BASE_SEPOLIA), "43"))
	if err != nil {
		t.Fatalf("EncodeTypedData: %v", err)
	}
	if signer := recoverSigner(t, hash.Bytes(), headers.Get("X-Signature")); signer != testKeyAddress(t) {
		t.Errorf("X-Signature recovers to %s, want %s", signer, testKeyAddress(t))
	}
}

func TestPremiumCancelRejectsBadNonce(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	err := runPremium(
		"cancel",
		"--url", server.URL,
		"--id", "9f31",
		"--chain_id", strconv.Itoa(CHAIN_ID_BASE_SEPOLIA),
		"--nonce", "0x2b",
		"--private_key", testPrivateKey,
	)
	if err == nil {
		t.Error("expected an error for a non decimal nonce")
	}
	if calls != 0 {
		t.Errorf("made %d requests, want none", calls)
	}
}

func TestPremiumRateLimitAndErrorStatuses(t *testing.T) {
	status := http.StatusTooManyRequests
	body := "cc"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	defer server.Close()

	err := runPremium("requests", "--maker", "0x1000000000000000000000000000000000000001", "--url", server.URL)
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("got %v, want a rate limit error", err)
	}

	status, body = http.StatusBadRequest, "missing address"
	err = runPremium("requests", "--maker", "0x1000000000000000000000000000000000000001", "--url", server.URL)
	if err == nil || !strings.Contains(err.Error(), "missing address") {
		t.Errorf("got %v, want the api's message", err)
	}
}

func TestPremiumQuoteEmptyBodyIsAFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// a recovered panic on the api answers 200 with no body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	inputs := []premiumQuoteInput{testPremiumQuoteInput(t)}
	batch, err := json.Marshal(inputs)
	if err != nil {
		t.Fatalf("marshal batch: %v", err)
	}
	file := t.TempDir() + "/batch.json"
	if err := os.WriteFile(file, batch, 0o600); err != nil {
		t.Fatalf("write batch: %v", err)
	}

	_, err = captureStdout(t, func() error {
		return runPremium("quote", "--url", server.URL, "--batch", file, "--private_key", testPrivateKey)
	})
	if err == nil || !strings.Contains(err.Error(), "empty response body") {
		t.Errorf("got %v, want an empty body failure", err)
	}
}
