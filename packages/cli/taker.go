package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"github.com/goccy/go-json"
)

// Taker commands for the v12 websocket flow. They run inside a `connect` to the
// taker endpoint: `request` opens an auction and the answer is the winning
// quote, `confirm` takes that quote and the answer is the settling tx hash.

// connectURL is the websocket a running connect dialled, so a command inside it
// can reach the same api over http.
var connectURL string

var inventoryAction = &Command{
	Name:  "inventory",
	Usage: "list the expiries and strikes that can be requested",
	Action: func(c *Context) error {
		return send(JsonRPCRequest{JsonRPC: "2.0", ID: "inventory", Method: "inventory"})
	},
}

// v12Request is what a taker sends to open an auction. It is not signed: the
// taker commits with the confirmation.
type v12Request struct {
	Asset           string `json:"asset"`
	ChainID         int    `json:"chainId"`
	Expiry          int64  `json:"expiry"`
	IsPut           bool   `json:"isPut"`
	IsTakerBuy      bool   `json:"isTakerBuy"`
	Quantity        string `json:"quantity"`
	Strike          string `json:"strike"`
	Taker           string `json:"taker"`
	USD             string `json:"usd"`
	CollateralAsset string `json:"collateralAsset"`
}

var requestAction = &Command{
	Name:  "request",
	Usage: "open an rfq; the answer is the auction's winning quote",
	Flags: []Flag{
		&StringFlag{Name: "asset", Required: true, Usage: "asset address"},
		&IntFlag{Name: "chain_id", Required: true},
		&Int64Flag{Name: "expiry", Required: true, Usage: "a listed expiry, unix seconds"},
		&BoolFlag{Name: "is_put", Usage: "present for a put"},
		&StringFlag{Name: "quantity", Required: true, Usage: "option quantity (1e18)"},
		&StringFlag{Name: "strike", Required: true, Usage: "a listed strike (1e8)"},
		&StringFlag{Name: "taker", Required: true, Usage: "taker address, the one that will confirm"},
		&StringFlag{Name: "usd", Required: true, Usage: "strike asset address"},
		&StringFlag{Name: "collateral", Required: true, Usage: "collateral asset address"},
	},
	Action: func(c *Context) error {
		for _, name := range []string{"asset", "taker", "usd", "collateral"} {
			if !common.IsHexAddress(c.String(name)) {
				return fmt.Errorf("invalid %s %q", name, c.String(name))
			}
		}
		return send(JsonRPCRequest{
			JsonRPC: "2.0",
			ID:      "request",
			Method:  "request",
			Params: v12Request{
				Asset:           c.String("asset"),
				ChainID:         c.Int("chain_id"),
				Expiry:          c.Int64("expiry"),
				IsPut:           c.Bool("is_put"),
				Quantity:        c.String("quantity"),
				Strike:          c.String("strike"),
				Taker:           c.String("taker"),
				USD:             c.String("usd"),
				CollateralAsset: c.String("collateral"),
			},
		})
	},
}

var confirmAction = &Command{
	Name:  "confirm",
	Usage: "take a quote an rfq won; the answer is the settling tx hash",
	Flags: []Flag{
		&StringFlag{Name: "quote", Required: true, Usage: "the winning quote, as json, exactly as the request's answer carried it"},
		&Uint64Flag{Name: "nonce", Required: true, Usage: "unused confirmation nonce for this taker"},
		&StringFlag{
			Name:     "private_key",
			Required: true,
			Usage:    "private key of the request's taker",
			EnvVars:  []string{"RYSK_PRIVATE_KEY"},
		},
		&StringFlag{Name: "api_url", Usage: "http base of the api, for the gas fee; defaults to the connect's host"},
	},
	Action: func(c *Context) error {
		return confirm(c)
	},
}

// v12Confirmation is the confirmation the taker endpoint takes: the signed
// Confirmation fields, plus validUntil and quoteValidUntil, which are not
// signed.
type v12Confirmation struct {
	Maker            string `json:"maker"`
	AssetAddress     string `json:"assetAddress"`
	ChainID          int    `json:"chainId"`
	Expiry           int64  `json:"expiry"`
	IsPut            bool   `json:"isPut"`
	Nonce            string `json:"nonce"`
	Price            string `json:"price"`
	Quantity         string `json:"quantity"`
	QuoteNonce       string `json:"quoteNonce"`
	QuoteSignature   string `json:"quoteSignature"`
	Strike           string `json:"strike"`
	Taker            string `json:"taker"`
	IsTakerBuy       bool   `json:"isTakerBuy"`
	USD              string `json:"usd"`
	CollateralAsset  string `json:"collateralAsset"`
	CollateralAmount string `json:"collateralAmount"`
	GasFee           string `json:"gasFee"`
	ValidUntil       int64  `json:"validUntil"`
	QuoteValidUntil  int64  `json:"quoteValidUntil"`
	Signature        string `json:"signature,omitempty"`
}

func confirm(c *Context) error {
	var q Quote
	if err := json.Unmarshal([]byte(c.String("quote")), &q); err != nil {
		return fmt.Errorf("invalid quote: %w", err)
	}
	if q.Signature == "" {
		return errors.New("invalid quote: it has no signature, pass the request's answer as it came")
	}
	privateKey := c.String("private_key")
	taker, err := newAccountFromPrivateKey(privateKey)
	if err != nil {
		return err
	}
	api, err := takerAPIURL(c.String("api_url"))
	if err != nil {
		return err
	}

	conf := v12Confirmation{
		Maker:           q.Maker,
		AssetAddress:    q.AssetAddress,
		ChainID:         q.ChainID,
		Expiry:          q.Expiry,
		IsPut:           q.IsPut,
		Nonce:           strconv.FormatUint(c.Uint64("nonce"), 10),
		Price:           q.Price,
		Quantity:        q.Quantity,
		QuoteNonce:      q.Nonce,
		QuoteSignature:  q.Signature,
		Strike:          q.Strike,
		Taker:           strings.ToLower(taker.Public.Hex()),
		IsTakerBuy:      q.IsTakerBuy,
		USD:             q.USD,
		CollateralAsset: q.CollateralAsset,
		// the api turns a confirmation down once its validUntil has passed; the
		// quote's own validUntil bounds it anyway
		ValidUntil:      time.Now().Add(time.Minute).Unix(),
		QuoteValidUntil: q.ValidUntil,
	}
	if conf.CollateralAmount, err = collateralAmount(api, q); err != nil {
		return err
	}
	// the gas fee is signed, and the api refuses one below its own estimate
	if conf.GasFee, err = gasFee(api, conf); err != nil {
		return err
	}

	message := map[string]any{
		"maker":            conf.Maker,
		"assetAddress":     conf.AssetAddress,
		"chainId":          strconv.Itoa(conf.ChainID),
		"expiry":           strconv.FormatInt(conf.Expiry, 10),
		"isPut":            conf.IsPut,
		"nonce":            conf.Nonce,
		"price":            conf.Price,
		"quantity":         conf.Quantity,
		"quoteNonce":       conf.QuoteNonce,
		"quoteSignature":   conf.QuoteSignature,
		"strike":           conf.Strike,
		"taker":            conf.Taker,
		"isTakerBuy":       conf.IsTakerBuy,
		"usd":              conf.USD,
		"collateralAsset":  conf.CollateralAsset,
		"collateralAmount": conf.CollateralAmount,
		"gasFee":           conf.GasFee,
	}
	hash, err := EncodeTypedData(&apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": (*EIP712_TYPES)["EIP712Domain"],
			"Confirmation": confirmationType,
		},
		PrimaryType: "Confirmation",
		Domain:      *createEIP712Domain(int64(conf.ChainID)),
		Message:     message,
	})
	if err != nil {
		return err
	}
	if conf.Signature, err = Sign(hash.Bytes(), privateKey); err != nil {
		return err
	}
	return send(JsonRPCRequest{JsonRPC: "2.0", ID: "confirm", Method: "confirm", Params: conf})
}

// takerAPIURL is the http base the gas fee and assets are read from: given, or
// the host the running connect dialled.
func takerAPIURL(given string) (string, error) {
	if given != "" {
		return strings.TrimRight(given, "/"), nil
	}
	u, err := url.Parse(connectURL)
	if err != nil || u.Host == "" {
		return "", errors.New("no api url: pass --api_url, or run confirm inside a connect")
	}
	scheme := "https"
	if u.Scheme == "ws" {
		scheme = "http"
	}
	return scheme + "://" + u.Host, nil
}

// collateralAmount is what the taker locks, in the collateral's decimals: the
// quantity for a call, strike times quantity in the 6 decimal strike asset for a
// put. The api recomputes it and turns down any other value.
func collateralAmount(api string, q Quote) (string, error) {
	quantity, ok := new(big.Int).SetString(q.Quantity, 10)
	if !ok {
		return "", fmt.Errorf("invalid quantity %q", q.Quantity)
	}
	if q.IsPut {
		strike, ok := new(big.Int).SetString(q.Strike, 10)
		if !ok {
			return "", fmt.Errorf("invalid strike %q", q.Strike)
		}
		// quantity e18 * strike e8 / 1e26 * 1e6
		amount := new(big.Int).Mul(quantity, strike)
		return amount.Div(amount, new(big.Int).Exp(big.NewInt(10), big.NewInt(20), nil)).String(), nil
	}

	var assets map[string][]struct {
		Address  string `json:"address"`
		Decimals int64  `json:"decimals"`
	}
	if err := takerHTTP(http.MethodGet, api+"/api/assets", nil, &assets); err != nil {
		return "", err
	}
	for _, a := range assets[strconv.Itoa(q.ChainID)] {
		if strings.EqualFold(a.Address, q.AssetAddress) {
			if a.Decimals >= 18 {
				return quantity.String(), nil
			}
			scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18-a.Decimals), nil)
			return quantity.Div(quantity, scale).String(), nil
		}
	}
	return "", fmt.Errorf("asset %s is not listed on chain %d", q.AssetAddress, q.ChainID)
}

// gasFee is the api's estimate for settling this confirmation.
func gasFee(api string, conf v12Confirmation) (string, error) {
	body, err := json.Marshal(conf)
	if err != nil {
		return "", err
	}
	var estimated struct {
		GasFee string `json:"gasFee"`
	}
	if err := takerHTTP(http.MethodPost, api+"/api/gas", body, &estimated); err != nil {
		return "", err
	}
	return estimated.GasFee, nil
}

func takerHTTP(method string, endpoint string, body []byte, into any) error {
	req, err := http.NewRequest(method, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, endpoint, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s answered %d: %s", method, endpoint, res.StatusCode, strings.TrimSpace(string(data)))
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("%s %s: %w (%s)", method, endpoint, err, data)
	}
	return nil
}
