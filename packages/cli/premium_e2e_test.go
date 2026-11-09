package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// TestPremiumE2E trades an rfq through the premium api, end to end:
//
//  1. createRFQ: the taker opens a request only this maker may quote
//  2. pollRFQs: the maker polls its requests until that one shows up
//  3. postQuote: the maker signs and posts a quote on it
//  4. the maker deletes the quote, and it leaves the book
//  5. postQuote again, with a fresh nonce
//  6. takeTrade: the taker selects that quote, confirms it and executes it on
//     chain, and the quote leaves the book as taken
//
// It skips unless opted in, so plain `go test ./...` and ci never touch the
// network. `make e2e` runs it with the keys from the repo's .env:
//
//	RYSK_E2E_PRIVATE_KEY        maker key; its address is the maker
//	RYSK_E2E_TAKER_PRIVATE_KEY  an rfq signer on some pool, to open the request
//	                            and confirm the trade
//	RYSK_E2E_PREMIUM_URL        optional: defaults to the testnet api
//	RYSK_E2E_RPC_URL            optional: defaults to the public base sepolia rpc
//	RYSK_E2E_POOL               optional: the pool to trade against; defaults to
//	                            the first one the taker key is an rfq signer on
//
// The maker and taker have to be different keys: the api does not list a
// taker's own requests to it as a maker.
//
// The trade is real: the pool writes one far out of the money call to the maker
// and locks its collateral until expiry, and the maker pays a 1 wei premium. The
// maker needs gas and premium token on chain; the test approves the option
// handler to pull the premium once, the first time it runs for a maker.
//
// Nonces are unix nanoseconds: they are spent once per address, and the time
// keeps them fresh across runs without a stored counter.
func TestPremiumE2E(t *testing.T) {
	makerKey := os.Getenv("RYSK_E2E_PRIVATE_KEY")
	takerKey := os.Getenv("RYSK_E2E_TAKER_PRIVATE_KEY")
	if makerKey == "" || takerKey == "" {
		t.Skip("set RYSK_E2E_PRIVATE_KEY and RYSK_E2E_TAKER_PRIVATE_KEY to run against the testnet premium api")
	}
	apiURL := envOr("RYSK_E2E_PREMIUM_URL", "https://insti-testnet.rysk.finance")
	rpcURL := envOr("RYSK_E2E_RPC_URL", "https://sepolia.base.org")
	maker := e2eAddress(t, makerKey)

	// 1-2
	pool := createRFQ(t, apiURL, takerKey, maker)
	request := pollRFQs(t, apiURL, maker, pool.Address)

	// 3-4
	first := postQuote(t, apiURL, makerKey, maker, request)
	id := quoteID(t, apiURL, maker, first)
	if id == "" {
		t.Fatalf("quote with nonce %s is not in the maker's live quotes", first)
	}
	// a fresh nonce: the cancel's auth nonce is spent like any other
	cancelNonce := strconv.FormatInt(time.Now().UnixNano(), 10)
	e2ePremium(t, apiURL, "cancel", "--id", id, "--chain_id", strconv.Itoa(request.ChainID), "--nonce", cancelNonce, "--private_key", makerKey)
	if still := quoteID(t, apiURL, maker, first); still != "" {
		t.Fatalf("quote %s is still live after cancel", still)
	}

	// 5-6
	second := postQuote(t, apiURL, makerKey, maker, request)
	// the option handler pulls the premium with transferFrom when the trade
	// settles; 1 token covers a 1 wei premium per run for good
	e2eApprove(t, rpcURL, makerKey, request.ChainID, pool.Asset, pool.OptionHandler, big.NewInt(1), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	quote := selectQuote(t, apiURL, request.ID, second)
	txHash := takeTrade(t, apiURL, takerKey, quote)
	waitMined(t, rpcURL, txHash)
	t.Logf("traded quote %s on request %s: tx %s", quote.ID, request.ID, txHash)
	// a filled quote leaves the book, marked taken
	if still := quoteID(t, apiURL, maker, second); still != "" {
		t.Errorf("quote %s is still live after the trade", still)
	}
}

// e2ePool is a pool as GET /api/pools lists it, with what the test needs to open
// a request on it and settle a trade against it.
type e2ePool struct {
	Address       string `json:"address"`
	ChainID       int    `json:"chainId"`
	OptionHandler string `json:"optionHandler"`
	Asset         string `json:"asset"`
	Underlying    string `json:"underlying"`
	Type          string `json:"type"`
}

// e2eRequest is the part of a listed request a quote is built from. The api
// sends more; only these fields are signed or matched on.
type e2eRequest struct {
	ID              string              `json:"id"`
	Asset           string              `json:"asset"`
	ChainID         int                 `json:"chainId"`
	Expiry          int64               `json:"expiry"`
	IsPut           bool                `json:"isPut"`
	IsTakerBuy      bool                `json:"isTakerBuy"`
	Quantity        string              `json:"quantity"`
	Strike          string              `json:"strike"`
	Taker           string              `json:"taker"`
	USD             string              `json:"usd"`
	CollateralAsset string              `json:"collateralAsset"`
	TypeDataDomain  *premiumDomainInput `json:"typeDataDomain"`
}

// e2eQuote is a quote as a request's book lists it to the taker.
type e2eQuote struct {
	ID    string `json:"id"`
	Nonce string `json:"nonce"`
}

// createRFQ plays the taker: it finds a pool the taker key is an rfq signer on
// and opens a request on it only the maker may quote. It returns the pool, which
// is the request's taker: a request's taker is always a pool, never the signer.
//
// The request is not cancelled. A trade leaves it active, and it expires on its
// own after five minutes; until then the sdk suites that `make e2e` runs next
// can quote it too.
func createRFQ(t *testing.T, apiURL string, takerKey string, maker string) e2ePool {
	t.Helper()
	signer := e2eAddress(t, takerKey)
	client := newPremiumClient(apiURL)

	// Only a key in a pool's on chain rfqSigners may open its requests, and the
	// api cannot add one. Take the first pool that lists this signer, rather than
	// pinning a pool that testnet may retire. A pool with no option handler ("0x")
	// is not set up for rfqs, and its signer check errors.
	var pools []e2ePool
	if err := json.Unmarshal([]byte(e2ePremium(t, apiURL, "pools")), &pools); err != nil {
		t.Fatalf("pools: %v", err)
	}
	// RYSK_E2E_POOL pins one, for when the first signable pool cannot trade, say
	// because its option handler has not been upgraded.
	want := os.Getenv("RYSK_E2E_POOL")
	var chosen *e2ePool
	for i, p := range pools {
		if want != "" && !strings.EqualFold(p.Address, want) {
			continue
		}
		if p.OptionHandler != "0x" && e2eStatus(t, client, "/api/pools/"+p.Address+"/is-rfq-signer?signer="+signer) == http.StatusOK {
			chosen = &pools[i]
			break
		}
	}
	if chosen == nil {
		t.Skipf("%s is an rfq signer on no pool at %s, so it cannot open a request", signer, apiURL)
	}

	// next friday 08:00 utc at least a week out, the usual option expiry
	expiry := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 7)
	for expiry.Weekday() != time.Friday {
		expiry = expiry.AddDate(0, 0, 1)
	}
	expiry = expiry.Add(8 * time.Hour)

	// The api checks each field against the pool:
	//   - taker is the pool, never the signer
	//   - asset is what the pool writes options on, its underlying
	//   - collateralAsset and premiumAsset are assets the pool holds
	//   - isPut matches the contract type the pool writes
	//   - usd is an asset the api has registered as a strike asset on the chain
	//   - isPremium is required on every request in a post
	//   - strike and quantity are positive integers (1e8 and 1e18); the strike
	//     here is deliberately far out of the money, and the quantity is one
	//     option
	//   - expiry (unix seconds) is in the future, and validUntil (unix
	//     MILLISECONDS, unlike a quote's) is before it
	// isTakerBuy false: the pool sells, so it writes the option and the maker pays
	// the premium. makers restricts quoting to this maker, so no one else's quote
	// lands on the request.
	request := map[string]any{
		"asset":           chosen.Underlying,
		"chainId":         chosen.ChainID,
		"expiry":          expiry.Unix(),
		"isPut":           chosen.Type == "Put",
		"isTakerBuy":      false,
		"quantity":        "1000000000000000000",
		"strike":          "100000000000000",
		"taker":           chosen.Address,
		"usd":             strings.ToLower(ADDRESSES[chosen.ChainID].StrikeAsset.Hex()),
		"collateralAsset": chosen.Asset,
		"premiumAsset":    chosen.Asset,
		"isPremium":       true,
		"makers":          []string{maker},
		"validUntil":      time.Now().Add(5 * time.Minute).UnixMilli(),
	}
	batch, err := json.Marshal([]any{request})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	file := t.TempDir() + "/requests.json"
	if err := os.WriteFile(file, batch, 0o600); err != nil {
		t.Fatalf("write requests: %v", err)
	}
	// Opening a request is authenticated with the signer's auth headers. The api
	// answers 201 with no body: ids are server assigned, which is why the maker
	// has to find the request in its own listing.
	nonce := strconv.FormatInt(time.Now().UnixNano(), 10)
	e2ePremium(t, apiURL, "request", "--batch", file, "--chain_id", strconv.Itoa(chosen.ChainID), "--nonce", nonce, "--private_key", takerKey)
	return *chosen
}

// pollRFQs polls the maker's requests, as a maker does, until one from the pool
// shows up, and returns the newest. The api lists a request as soon as it is
// stored, so this normally returns on the first poll.
func pollRFQs(t *testing.T, apiURL string, maker string, pool string) e2eRequest {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var requests []e2eRequest
		// an empty body means none
		if out := e2ePremium(t, apiURL, "requests", "--maker", maker); out != "" {
			if err := json.Unmarshal([]byte(out), &requests); err != nil {
				t.Fatalf("requests: %v (%s)", err, out)
			}
		}
		requests = slices.DeleteFunc(requests, func(r e2eRequest) bool { return !strings.EqualFold(r.Taker, pool) })
		if len(requests) > 0 {
			return requests[len(requests)-1]
		}
		if time.Now().After(deadline) {
			t.Fatalf("no request from %s listed for %s after 30s", pool, maker)
		}
		// well under the api's 30 requests a second per ip
		time.Sleep(time.Second)
	}
}

// postQuote signs a quote on the request as the maker, posts it, and returns its
// nonce, the only handle on it until its id is looked up.
//
// The price is 1 wei per option, the smallest premium there is (the handler
// rounds it up to 1 wei of the premium token), so the trade costs the maker
// next to nothing.
func postQuote(t *testing.T, apiURL string, makerKey string, maker string, request e2eRequest) string {
	t.Helper()
	nonce := strconv.FormatInt(time.Now().UnixNano(), 10)

	// The terms are copied from the request untouched: the api rebuilds the
	// signed message from the stored request, so any altered term only yields a
	// signature that does not verify. The domain is the request's too, the pool's
	// option handler. It goes in as a batch file because that carries the domain
	// as json, where the flags would need it split apart.
	batch, err := json.Marshal([]premiumQuoteInput{{
		Quote: Quote{
			AssetAddress: request.Asset,
			ChainID:      request.ChainID,
			Expiry:       request.Expiry,
			IsPut:        request.IsPut,
			IsTakerBuy:   request.IsTakerBuy,
			Maker:        maker,
			Nonce:        nonce,
			Price:        "1",
			Quantity:     request.Quantity,
			Strike:       request.Strike,
			// unix seconds, strictly inside the api's now+2min .. now+10min window
			ValidUntil:      time.Now().Add(5 * time.Minute).Unix(),
			USD:             request.USD,
			CollateralAsset: request.CollateralAsset,
		},
		RequestID: request.ID,
		Domain:    request.TypeDataDomain,
	}})
	if err != nil {
		t.Fatalf("marshal batch: %v", err)
	}
	file := t.TempDir() + "/batch.json"
	if err := os.WriteFile(file, batch, 0o600); err != nil {
		t.Fatalf("write batch: %v", err)
	}

	// the post answers 200 either way; the cli exits non zero, failing the test,
	// when the response lists the quote under failures
	e2ePremium(t, apiURL, "quote", "--batch", file, "--private_key", makerKey)
	return nonce
}

// quoteID returns the id of the maker's quote with this nonce, or "" when it is
// not on the book. The post returns no ids, so the maker's book is the only
// place to get one, and since the book lists only live quotes, "" also means
// filled, cancelled or expired.
func quoteID(t *testing.T, apiURL string, maker string, nonce string) string {
	t.Helper()
	var quotes []map[string]any
	if out := e2ePremium(t, apiURL, "quotes", "--maker", maker); out != "" {
		if err := json.Unmarshal([]byte(out), &quotes); err != nil {
			t.Fatalf("quotes: %v (%s)", err, out)
		}
	}
	for _, q := range quotes {
		if fmt.Sprint(q["nonce"]) == nonce {
			return fmt.Sprint(q["id"])
		}
	}
	return ""
}

// selectQuote is the taker reading the request's book and picking the quote
// with this nonce. The book carries the maker's signature, which the
// confirmation has to embed.
func selectQuote(t *testing.T, apiURL string, requestID string, nonce string) e2eQuote {
	t.Helper()
	var quotes []e2eQuote
	if out := e2ePremium(t, apiURL, "request-quotes", "--id", requestID); out != "" {
		if err := json.Unmarshal([]byte(out), &quotes); err != nil {
			t.Fatalf("request-quotes: %v (%s)", err, out)
		}
	}
	for _, q := range quotes {
		if q.Nonce == nonce {
			return q
		}
	}
	t.Fatalf("quote with nonce %s is not on request %s's book (%d quotes)", nonce, requestID, len(quotes))
	return e2eQuote{}
}

// takeTrade is the taker accepting the quote: `premium execute` signs a
// confirmation embedding it and posts it to /api/execute, whose operator sends
// it to the option handler. It returns the transaction hash.
func takeTrade(t *testing.T, apiURL string, takerKey string, quote e2eQuote) string {
	t.Helper()
	nonce := strconv.FormatInt(time.Now().UnixNano(), 10)
	txHash := e2ePremium(t, apiURL, "execute", "--quote_id", quote.ID, "--nonce", nonce, "--private_key", takerKey)
	if !strings.HasPrefix(txHash, "0x") {
		t.Fatalf("execute printed %q, want a transaction hash", txHash)
	}
	return txHash
}

// e2eApprove lets spender pull asset from key's account, sending an approval for
// amount only when the allowance is below need, so most runs send nothing.
func e2eApprove(t *testing.T, rpcURL string, makerKey string, chainID int, asset string, spender string, need *big.Int, amount *big.Int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		t.Fatalf("dial %s: %v", rpcURL, err)
	}
	defer client.Close()
	account, err := newAccountFromPrivateKey(makerKey)
	if err != nil {
		t.Fatalf("maker key: %v", err)
	}
	allowance, err := erc20Allowance(&bind.CallOpts{Context: ctx}, erc20(common.HexToAddress(asset), client), account.Public, common.HexToAddress(spender))
	if err != nil {
		t.Fatalf("allowance: %v", err)
	}
	if allowance.Cmp(need) >= 0 {
		return
	}
	if _, err := account.approve(ctx, chainID, *client, amount, common.HexToAddress(asset), common.HexToAddress(spender)); err != nil {
		t.Fatalf("approve %s to %s: %v", asset, spender, err)
	}
}

// waitMined waits for the trade's transaction and fails the test unless it
// succeeded on chain.
func waitMined(t *testing.T, rpcURL string, txHash string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		t.Fatalf("dial %s: %v", rpcURL, err)
	}
	defer client.Close()
	rx, err := bind.WaitMinedHash(ctx, client, common.HexToHash(txHash))
	if err != nil {
		t.Fatalf("trade %s not mined after 2 minutes: %v", txHash, err)
	}
	if rx.Status != 1 {
		t.Fatalf("trade %s reverted", txHash)
	}
}

// e2eStatus is the status a GET answers with.
func e2eStatus(t *testing.T, client *premiumClient, path string) int {
	t.Helper()
	status, _, err := client.do(http.MethodGet, path, nil, nil, nil)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return status
}

// e2ePremium runs a premium subcommand in process, exactly as the binary would,
// and fails the test on a non zero exit. The api's response is its stdout.
func e2ePremium(t *testing.T, apiURL string, args ...string) string {
	t.Helper()
	out, err := captureStdout(t, func() error {
		return runPremium(append(args, "--url", apiURL)...)
	})
	if err != nil {
		t.Fatalf("premium %s: %v\n%s", args[0], err, out)
	}
	return strings.TrimSpace(out)
}

// e2eAddress is the lowercased address a hex private key signs as.
func e2eAddress(t *testing.T, privateKey string) string {
	t.Helper()
	account, err := newAccountFromPrivateKey(privateKey)
	if err != nil {
		t.Fatalf("invalid private key: %v", err)
	}
	return strings.ToLower(account.Public.Hex())
}

func envOr(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
