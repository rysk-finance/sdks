package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"github.com/goccy/go-json"
	"github.com/urfave/cli/v2"
)

const (
	PREMIUM_URL = "https://premium.rysk.finance"

	// The premium api signs against the pool's option handler, never the rysk
	// contract the websocket flow uses.
	PREMIUM_DOMAIN_NAME    = "PremiumOptionHandler"
	PREMIUM_DOMAIN_VERSION = "1"

	// Window the api accepts for a quote's valid until, exclusive at both ends.
	PREMIUM_QUOTE_MIN_VALIDITY = 2 * time.Minute
	PREMIUM_QUOTE_MAX_VALIDITY = 10 * time.Minute

	// A valid until this large is unix milliseconds, which requests use and
	// quotes do not.
	PREMIUM_MILLISECONDS_THRESHOLD = int64(1e12)
)

var premiumAction = &cli.Command{
	Name:  "premium",
	Usage: "maker actions against the premium rfq api",
	Subcommands: []*cli.Command{
		premiumRequestsAction,
		premiumQuoteAction,
		premiumQuotesAction,
		premiumQuoteStatusAction,
		premiumCancelAction,
	},
}

func premiumURLFlag() cli.Flag {
	return &cli.StringFlag{
		Name:  "url",
		Value: PREMIUM_URL,
		Usage: "base url of the premium rfq api",
	}
}

var premiumRequestsAction = &cli.Command{
	Name:  "requests",
	Usage: "list the live requests this maker may quote",
	Flags: []cli.Flag{
		premiumURLFlag(),
		&cli.StringFlag{
			Name:     "maker",
			Required: true,
			Usage:    "maker address to list requests for",
		},
	},
	Action: func(c *cli.Context) error {
		query := url.Values{"address": {strings.ToLower(c.String("maker"))}}
		return premiumProxy(c, http.MethodGet, "/api/requests/maker", query)
	},
}

var premiumQuotesAction = &cli.Command{
	Name:  "quotes",
	Usage: "list this maker's live quotes",
	Flags: []cli.Flag{
		premiumURLFlag(),
		&cli.StringFlag{
			Name:     "maker",
			Required: true,
			Usage:    "maker address to list quotes for",
		},
	},
	Action: func(c *cli.Context) error {
		query := url.Values{"address": {strings.ToLower(c.String("maker"))}}
		return premiumProxy(c, http.MethodGet, "/api/quotes", query)
	},
}

var premiumQuoteStatusAction = &cli.Command{
	Name:  "quote-status",
	Usage: "fetch one quote by id, whatever its status",
	Flags: []cli.Flag{
		premiumURLFlag(),
		&cli.StringFlag{
			Name:     "id",
			Required: true,
			Usage:    "quote id",
		},
	},
	Action: func(c *cli.Context) error {
		return premiumProxy(c, http.MethodGet, "/api/quotes/"+url.PathEscape(c.String("id")), nil)
	},
}

var premiumCancelAction = &cli.Command{
	Name:  "cancel",
	Usage: "pull one of this maker's quotes",
	Flags: []cli.Flag{
		premiumURLFlag(),
		&cli.StringFlag{
			Name:     "id",
			Required: true,
			Usage:    "quote id to cancel",
		},
		&cli.Int64Flag{
			Name:     "chain_id",
			Required: true,
			Usage:    "chain id to authenticate against",
		},
		&cli.StringFlag{
			Name:     "nonce",
			Required: true,
			Usage:    "unused nonce for this maker (decimal uint64), drawn from the same counter as your quote nonces",
		},
		&cli.StringFlag{
			Name:     "private_key",
			Required: true,
			Usage:    "private key of the quote's maker",
		},
	},
	Action: func(c *cli.Context) error {
		return premiumCancel(c)
	},
}

var premiumQuoteAction = &cli.Command{
	Name:  "quote",
	Usage: "sign and post quotes",
	Flags: []cli.Flag{
		premiumURLFlag(),
		&cli.StringFlag{
			Name:  "batch",
			Usage: `path to a json array of quotes to sign and post in one call, or "-" for stdin. Cannot be combined with the per quote flags`,
		},
		&cli.StringFlag{
			Name:  "request_id",
			Usage: "id of the request being quoted",
		},
		&cli.StringFlag{
			Name:  "asset",
			Usage: "asset address, as it came on the request",
		},
		&cli.IntFlag{
			Name:  "chain_id",
			Usage: "chain id, as it came on the request",
		},
		&cli.Int64Flag{
			Name:  "expiry",
			Usage: "option expiry in unix seconds, as it came on the request",
		},
		&cli.BoolFlag{
			Name:  "is_put",
			Usage: "present for a put, as it came on the request",
		},
		&cli.BoolFlag{
			Name:  "is_taker_buy",
			Usage: "present if the taker buys, as it came on the request",
		},
		&cli.StringFlag{
			Name:  "strike",
			Usage: "option strike (1e8), as it came on the request",
		},
		&cli.StringFlag{
			Name:  "quantity",
			Usage: "option quantity (1e18), as it came on the request",
		},
		&cli.StringFlag{
			Name:  "usd",
			Usage: "usd asset address, as it came on the request",
		},
		&cli.StringFlag{
			Name:  "collateral",
			Usage: "collateral asset address, as it came on the request",
		},
		&cli.StringFlag{
			Name:  "maker",
			Usage: "maker address, has to match the signing key",
		},
		&cli.StringFlag{
			Name:  "nonce",
			Usage: "unused nonce for this maker (decimal uint64)",
		},
		&cli.StringFlag{
			Name:  "price",
			Usage: "premium per unit (1e18), non zero",
		},
		&cli.Int64Flag{
			Name:  "valid_until",
			Usage: "quote expiry in unix seconds, strictly between now+2min and now+10min",
		},
		&cli.StringFlag{
			Name:  "private_key",
			Usage: "private key to sign the quote with",
		},
		&cli.StringFlag{
			Name:  "domain_name",
			Value: PREMIUM_DOMAIN_NAME,
			Usage: "EIP712 domain name",
		},
		&cli.StringFlag{
			Name:  "domain_version",
			Value: PREMIUM_DOMAIN_VERSION,
			Usage: "EIP712 domain version",
		},
		&cli.StringFlag{
			Name:  "domain_verifying_contract",
			Usage: "EIP712 domain verifying contract: the pool's option handler, from the request's typeDataDomain",
		},
	},
	Action: func(c *cli.Context) error {
		return premiumQuote(c)
	},
}

// premiumDomainInput is the EIP712 domain as a request carries it. Only the
// fields the api's domain actually uses are honoured: chainId is taken from the
// quote, and a salt cannot be signed at all.
type premiumDomainInput struct {
	Name              string                `json:"name"`
	Version           string                `json:"version"`
	ChainId           *math.HexOrDecimal256 `json:"chainId"`
	VerifyingContract string                `json:"verifyingContract"`
	Salt              string                `json:"salt"`
}

// premiumQuoteInput is one quote to sign: the request's terms, the maker's own
// fields, and the domain to sign against.
type premiumQuoteInput struct {
	Quote
	RequestID string              `json:"requestId"`
	Domain    *premiumDomainInput `json:"domain"`
}

// premiumQuotePayload is a quote as the api takes it. The option terms are not
// sent - the server rebuilds them from the stored request - so a term that does
// not match the request simply yields a signature that will not verify.
type premiumQuotePayload struct {
	RequestID  string `json:"requestId"`
	Maker      string `json:"maker"`
	Price      string `json:"price"`
	Nonce      string `json:"nonce"`
	ValidUntil int64  `json:"validUntil"`
	Signature  string `json:"signature"`
}

type premiumClient struct {
	baseURL string
	http    *http.Client
}

func newPremiumClient(baseURL string) *premiumClient {
	if baseURL == "" {
		baseURL = PREMIUM_URL
	}
	return &premiumClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (client *premiumClient) do(method string, path string, query url.Values, body []byte, headers map[string]string) (int, []byte, error) {
	endpoint := client.baseURL + path
	if len(query) > 0 {
		endpoint = fmt.Sprintf("%s?%s", endpoint, query.Encode())
	}

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("invalid request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}

	res, err := client.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("request to %s failed: %w", endpoint, err)
	}
	defer res.Body.Close()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return res.StatusCode, nil, fmt.Errorf("failed to read response: %w", err)
	}
	return res.StatusCode, data, nil
}

// premiumStatusError maps the api's failure statuses onto errors. A rate limited
// response carries the body "cc", which is worth spelling out.
func premiumStatusError(status int, body []byte) error {
	trimmed := strings.TrimSpace(string(body))
	switch {
	case status == http.StatusTooManyRequests:
		return fmt.Errorf("rate limited (%d): the api allows 30 requests/second per ip, body %q", status, trimmed)
	case status >= 200 && status < 300:
		return nil
	default:
		return fmt.Errorf("api returned %d: %s", status, trimmed)
	}
}

// premiumProxy runs a read and prints the api's response as it came, so callers
// keep working when the api grows a field.
func premiumProxy(c *cli.Context, method string, path string, query url.Values) error {
	status, body, err := newPremiumClient(c.String("url")).do(method, path, query, nil, nil)
	if err != nil {
		return err
	}
	if err := premiumStatusError(status, body); err != nil {
		return err
	}

	if trimmed := strings.TrimSpace(string(body)); trimmed != "" {
		fmt.Println(trimmed)
	}
	return nil
}

func premiumCancel(c *cli.Context) error {
	nonce := c.String("nonce")
	if _, err := strconv.ParseUint(nonce, 10, 64); err != nil {
		return fmt.Errorf("invalid nonce %q: has to be a decimal uint64", nonce)
	}

	headers, err := premiumAuthHeaders(c.Int64("chain_id"), nonce, c.String("private_key"))
	if err != nil {
		return err
	}

	path := "/api/quotes/" + url.PathEscape(c.String("id"))
	status, body, err := newPremiumClient(c.String("url")).do(http.MethodDelete, path, nil, nil, headers)
	if err != nil {
		return err
	}
	if err := premiumStatusError(status, body); err != nil {
		return err
	}

	if trimmed := strings.TrimSpace(string(body)); trimmed != "" {
		fmt.Println(trimmed)
	}
	return nil
}

// premiumAuthHeaders signs the Authentication message the api's wallet
// authenticated routes expect. Its domain is deliberately not the trade domain,
// so an auth signature can never be replayed as a quote. Every nonce works once,
// across auth and quotes alike, so draw them from a single counter per address.
func premiumAuthHeaders(chainId int64, nonce string, privateKey string) (map[string]string, error) {
	hash, err := EncodeTypedData(premiumAuthTypedData(chainId, nonce))
	if err != nil {
		return nil, err
	}
	signature, err := Sign(hash.Bytes(), privateKey)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"X-Chain-Id":  strconv.FormatInt(chainId, 10),
		"X-Nonce":     nonce,
		"X-Signature": signature,
	}, nil
}

func premiumAuthTypedData(chainId int64, nonce string) *apitypes.TypedData {
	return &apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain":   (*EIP712_TYPES)["EIP712Domain"],
			"Authentication": {{Name: "nonce", Type: "string"}},
		},
		PrimaryType: "Authentication",
		Domain: apitypes.TypedDataDomain{
			Name:              "Authentication",
			Version:           "0.0.0",
			ChainId:           math.NewHexOrDecimal256(chainId),
			VerifyingContract: ZeroAddress.String(),
		},
		// the nonce is signed as a decimal string and has to match X-Nonce byte for byte
		Message: map[string]interface{}{"nonce": nonce},
	}
}

func premiumQuote(c *cli.Context) error {
	privateKey := c.String("private_key")
	if privateKey == "" {
		return errors.New("missing private key")
	}

	inputs, err := premiumQuoteInputs(c)
	if err != nil {
		return err
	}

	// Everything is signed before anything is posted, so a bad entry in a batch
	// cannot leave half a strip on the book.
	now := time.Now()
	payloads := make([]premiumQuotePayload, 0, len(inputs))
	for i, input := range inputs {
		payload, err := premiumPrepareQuote(input, privateKey, now)
		if err != nil {
			if len(inputs) == 1 {
				return err
			}
			return fmt.Errorf("quote %d: %w", i, err)
		}
		payloads = append(payloads, payload)
	}

	body, err := json.Marshal(payloads)
	if err != nil {
		return fmt.Errorf("invalid payload: %w", err)
	}

	status, res, err := newPremiumClient(c.String("url")).do(http.MethodPost, "/api/quotes", nil, body, nil)
	if err != nil {
		return err
	}
	if err := premiumStatusError(status, res); err != nil {
		return err
	}
	return premiumQuoteResult(res, len(payloads))
}

// premiumQuoteInputs collects the quotes to sign, either from a batch file or
// from the per quote flags.
func premiumQuoteInputs(c *cli.Context) ([]premiumQuoteInput, error) {
	batch := c.String("batch")
	if batch == "" {
		return []premiumQuoteInput{{
			Quote: Quote{
				AssetAddress:    c.String("asset"),
				ChainID:         c.Int("chain_id"),
				Expiry:          c.Int64("expiry"),
				IsPut:           c.Bool("is_put"),
				IsTakerBuy:      c.Bool("is_taker_buy"),
				Maker:           c.String("maker"),
				Nonce:           c.String("nonce"),
				Price:           c.String("price"),
				Quantity:        c.String("quantity"),
				Strike:          c.String("strike"),
				ValidUntil:      c.Int64("valid_until"),
				USD:             c.String("usd"),
				CollateralAsset: c.String("collateral"),
			},
			RequestID: c.String("request_id"),
			Domain: &premiumDomainInput{
				Name:              c.String("domain_name"),
				Version:           c.String("domain_version"),
				VerifyingContract: c.String("domain_verifying_contract"),
			},
		}}, nil
	}

	perQuoteFlags := []string{
		"request_id", "asset", "chain_id", "expiry", "is_put", "is_taker_buy",
		"strike", "quantity", "usd", "collateral", "maker", "nonce", "price",
		"valid_until", "domain_name", "domain_version", "domain_verifying_contract",
	}
	for _, flag := range perQuoteFlags {
		if c.IsSet(flag) {
			return nil, fmt.Errorf("--batch cannot be combined with --%s", flag)
		}
	}

	data, err := premiumReadBatch(batch)
	if err != nil {
		return nil, err
	}

	var inputs []premiumQuoteInput
	if err := json.Unmarshal(data, &inputs); err != nil {
		return nil, fmt.Errorf("invalid batch json: %w", err)
	}
	if len(inputs) == 0 {
		return nil, errors.New("batch is empty")
	}
	return inputs, nil
}

func premiumReadBatch(source string) ([]byte, error) {
	if source == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("failed to read batch from stdin: %w", err)
		}
		return data, nil
	}

	data, err := os.ReadFile(source)
	if err != nil {
		return nil, fmt.Errorf("failed to read batch: %w", err)
	}
	return data, nil
}

// premiumPrepareQuote validates one quote and signs it. Validation happens
// locally to turn the api's rejections into immediate errors, but the api stays
// the authority - the valid until window is measured against this machine's
// clock.
func premiumPrepareQuote(input premiumQuoteInput, privateKey string, now time.Time) (premiumQuotePayload, error) {
	var payload premiumQuotePayload

	if input.RequestID == "" {
		return payload, errors.New("missing request id")
	}
	for name, address := range map[string]string{
		"maker":            input.Maker,
		"asset":            input.AssetAddress,
		"usd":              input.USD,
		"collateral asset": input.CollateralAsset,
	} {
		if !common.IsHexAddress(address) {
			return payload, fmt.Errorf("invalid %s %q", name, address)
		}
	}
	if input.ChainID == 0 {
		return payload, errors.New("missing chain id")
	}
	if _, err := strconv.ParseUint(input.Nonce, 10, 64); err != nil {
		return payload, fmt.Errorf("invalid nonce %q: has to be a decimal uint64", input.Nonce)
	}
	if err := premiumValidatePrice(input.Price); err != nil {
		return payload, err
	}
	if err := premiumValidateValidUntil(input.ValidUntil, now); err != nil {
		return payload, err
	}

	domain, err := premiumQuoteDomain(input.ChainID, input.Domain)
	if err != nil {
		return payload, err
	}

	messageHash, _, err := CreateQuoteMessage(input.Quote, domain)
	if err != nil {
		return payload, err
	}
	signature, err := Sign(messageHash, privateKey)
	if err != nil {
		return payload, err
	}

	return premiumQuotePayload{
		RequestID:  input.RequestID,
		Maker:      strings.ToLower(input.Maker),
		Price:      input.Price,
		Nonce:      input.Nonce,
		ValidUntil: input.ValidUntil,
		Signature:  signature,
	}, nil
}

// premiumQuoteDomain builds the domain to sign against, defaulting the name and
// version and always taking the chain from the quote.
func premiumQuoteDomain(chainId int, input *premiumDomainInput) (*apitypes.TypedDataDomain, error) {
	if input == nil {
		input = &premiumDomainInput{}
	}

	if input.Salt != "" {
		return nil, errors.New("domain salt is not supported")
	}
	if input.ChainId != nil {
		if given := (*big.Int)(input.ChainId); given.Int64() != int64(chainId) {
			return nil, fmt.Errorf("domain chain id %s does not match the quote's chain %d", given, chainId)
		}
	}
	if !common.IsHexAddress(input.VerifyingContract) {
		return nil, fmt.Errorf(
			"invalid domain verifying contract %q: pass the pool's option handler, as the request's typeDataDomain carries it",
			input.VerifyingContract,
		)
	}

	name := input.Name
	if name == "" {
		name = PREMIUM_DOMAIN_NAME
	}
	version := input.Version
	if version == "" {
		version = PREMIUM_DOMAIN_VERSION
	}

	return &apitypes.TypedDataDomain{
		Name:              name,
		Version:           version,
		ChainId:           math.NewHexOrDecimal256(int64(chainId)),
		VerifyingContract: input.VerifyingContract,
	}, nil
}

func premiumValidatePrice(price string) error {
	parsed, ok := math.ParseBig256(price)
	if price == "" || !ok {
		return fmt.Errorf("invalid price %q", price)
	}
	if parsed.Sign() == 0 {
		return errors.New("price is 0")
	}
	return nil
}

func premiumValidateValidUntil(validUntil int64, now time.Time) error {
	if validUntil >= PREMIUM_MILLISECONDS_THRESHOLD {
		return fmt.Errorf("valid until %d looks like unix milliseconds: quotes take unix seconds, requests take milliseconds", validUntil)
	}

	earliest := now.Add(PREMIUM_QUOTE_MIN_VALIDITY).Unix()
	latest := now.Add(PREMIUM_QUOTE_MAX_VALIDITY).Unix()
	if validUntil <= earliest || validUntil >= latest {
		return fmt.Errorf(
			"valid until %d is outside the api's window: it has to sit strictly between %d and %d (now+2min .. now+10min)",
			validUntil, earliest, latest,
		)
	}
	return nil
}

// premiumQuoteResult prints the api's response and fails the command when a
// quote was rejected: posting quotes answers 200 either way, with the rejections
// in a failures array, and an empty body is a recovered panic rather than a
// success.
func premiumQuoteResult(body []byte, posted int) error {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return errors.New("empty response body: treat the post as failed and retry with a fresh nonce")
	}
	fmt.Println(trimmed)

	var result struct {
		Failures []struct {
			Error string `json:"error"`
		} `json:"failures"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("unexpected response body: %w", err)
	}

	if len(result.Failures) > 0 {
		return fmt.Errorf("%d of %d quotes rejected: %s", len(result.Failures), posted, result.Failures[0].Error)
	}
	return nil
}
