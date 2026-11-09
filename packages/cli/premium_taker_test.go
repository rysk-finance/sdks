package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"github.com/goccy/go-json"
)

// execute builds the confirmation from the api's own quote, request and pool,
// signs it under the pool's option handler, and prints the tx hash.
func TestPremiumExecuteSignsTheStoredQuote(t *testing.T) {
	const pool = "0x1000000000000000000000000000000000000001"
	var posted map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/quotes/7":
			w.Write([]byte(`{"id":"7","requestId":"r1","maker":"0x3000000000000000000000000000000000000003","price":"5","nonce":"11","signature":"0xab","validUntil":1767225000}`))
		case "GET /api/requests/r1":
			w.Write([]byte(`{"id":"r1","asset":"0x98d56648c9b7f3cb49531f4135115b5000ab1733","chainId":84532,"expiry":1767225600,"isPut":true,"isTakerBuy":false,"quantity":"1000000000000000000","strike":"300000000000","taker":"` + pool + `","usd":"0x98d56648c9b7f3cb49531f4135115b5000ab1733","collateralAsset":"0x98d56648c9b7f3cb49531f4135115b5000ab1733","premiumAsset":"0x4000000000000000000000000000000000000004"}`))
		case "GET /api/pools/" + pool:
			w.Write([]byte(`{"address":"` + pool + `","optionHandler":"` + testOptionHandler + `"}`))
		case "POST /api/execute":
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &posted); err != nil {
				t.Errorf("execute body: %v", err)
			}
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte("0xfeed"))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	out, err := captureStdout(t, func() error {
		return runPremium("execute", "--quote_id", "7", "--nonce", "99", "--private_key", testPrivateKey, "--url", server.URL)
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.TrimSpace(out) != "0xfeed" {
		t.Errorf("printed %q, want the tx hash", out)
	}

	for field, want := range map[string]any{
		"signer":          strings.ToLower(testKeyAddress(t).Hex()),
		"premiumAsset":    "0x4000000000000000000000000000000000000004",
		"quoteValidUntil": float64(1767225000),
		"quoteSignature":  "0xab",
		"taker":           pool,
		"nonce":           "99",
		"chainId":         float64(84532),
	} {
		if posted[field] != want {
			t.Errorf("%s = %v, want %v", field, posted[field], want)
		}
	}

	message := map[string]any{}
	for _, f := range confirmationType {
		message[f.Name] = posted[f.Name]
	}
	message["chainId"] = strconv.Itoa(int(posted["chainId"].(float64)))
	message["expiry"] = strconv.Itoa(int(posted["expiry"].(float64)))
	domain, _ := premiumQuoteDomain(84532, &premiumDomainInput{VerifyingContract: testOptionHandler})
	hash, err := EncodeTypedData(&apitypes.TypedData{
		Types:       apitypes.Types{"EIP712Domain": (*EIP712_TYPES)["EIP712Domain"], "Confirmation": confirmationType},
		PrimaryType: "Confirmation",
		Domain:      *domain,
		Message:     message,
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if signer := recoverSigner(t, hash.Bytes(), posted["signature"].(string)); signer != testKeyAddress(t) {
		t.Errorf("confirmation recovers to %s, want %s", signer, testKeyAddress(t))
	}
}

// request forwards the batch verbatim behind auth headers, and refuses one
// that is not a json array before calling out.
func TestPremiumRequestForwardsTheBatch(t *testing.T) {
	var body string
	var nonce string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body, nonce = string(b), r.Header.Get("X-Nonce")
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	file := t.TempDir() + "/requests.json"
	writeFile(t, file, `[{"taker":"0x1","newField":true}]`)
	if err := runPremium("request", "--batch", file, "--chain_id", "84532", "--nonce", "5", "--private_key", testPrivateKey, "--url", server.URL); err != nil {
		t.Fatalf("request: %v", err)
	}
	if body != `[{"taker":"0x1","newField":true}]` || nonce != "5" {
		t.Errorf("forwarded %q with nonce %q", body, nonce)
	}

	writeFile(t, file, `{"taker":"0x1"}`)
	body = ""
	if err := runPremium("request", "--batch", file, "--chain_id", "84532", "--nonce", "6", "--private_key", testPrivateKey, "--url", server.URL); err == nil || body != "" {
		t.Errorf("a non array batch was sent (err %v)", err)
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
