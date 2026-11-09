package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Taker commands. A request's taker is always a pool; the key that signs for it
// has to be one of the pool's on chain rfq signers.

var premiumPoolsAction = &Command{
	Name:  "pools",
	Usage: "list the pools requests can be opened on",
	Flags: []Flag{premiumURLFlag()},
	Action: func(c *Context) error {
		return premiumProxy(c, http.MethodGet, "/api/pools", nil, nil, nil)
	},
}

var premiumTakerRequestsAction = &Command{
	Name:  "taker-requests",
	Usage: "list a pool's live requests",
	Flags: []Flag{
		premiumURLFlag(),
		&StringFlag{
			Name:     "taker",
			Required: true,
			Usage:    "pool address the requests were opened for",
		},
	},
	Action: func(c *Context) error {
		query := url.Values{"address": {strings.ToLower(c.String("taker"))}}
		return premiumProxy(c, http.MethodGet, "/api/requests/taker", query, nil, nil)
	},
}

var premiumRequestQuotesAction = &Command{
	Name:  "request-quotes",
	Usage: "list the live quotes on one request",
	Flags: []Flag{
		premiumURLFlag(),
		&StringFlag{
			Name:     "id",
			Required: true,
			Usage:    "request id",
		},
	},
	Action: func(c *Context) error {
		return premiumProxy(c, http.MethodGet, "/api/requests/"+url.PathEscape(c.String("id"))+"/quotes", nil, nil, nil)
	},
}

// premiumAuthFlags are what an auth header signed write takes.
func premiumAuthFlags(who string) []Flag {
	return []Flag{
		&Int64Flag{
			Name:     "chain_id",
			Required: true,
			Usage:    "chain id to authenticate against",
		},
		&StringFlag{
			Name:     "nonce",
			Required: true,
			Usage:    "unused nonce for this signer (decimal uint64)",
		},
		&StringFlag{
			Name:     "private_key",
			Required: true,
			Usage:    "private key of " + who,
			EnvVars:  []string{"RYSK_PRIVATE_KEY"},
		},
	}
}

var premiumRequestAction = &Command{
	Name:  "request",
	Usage: "open requests on a pool",
	Flags: append([]Flag{
		premiumURLFlag(),
		&StringFlag{
			Name:     "batch",
			Required: true,
			Usage:    `path to a json array of requests as the api takes them, or "-" for stdin`,
		},
	}, premiumAuthFlags("one of the pool's rfq signers")...),
	Action: func(c *Context) error {
		body, err := premiumReadBatch(c.String("batch"))
		if err != nil {
			return err
		}
		// sent as written, so a field the api grows needs no cli release
		var requests []json.RawMessage
		if err := json.Unmarshal(body, &requests); err != nil || len(requests) == 0 {
			return errors.New("batch has to be a non empty json array of requests")
		}
		return premiumAuthed(c, http.MethodPost, "/api/requests", body)
	},
}

var premiumCancelRequestAction = &Command{
	Name:  "cancel-request",
	Usage: "cancel a request and every quote on it",
	Flags: append([]Flag{
		premiumURLFlag(),
		&StringFlag{
			Name:     "id",
			Required: true,
			Usage:    "request id to cancel",
		},
	}, premiumAuthFlags("one of the pool's rfq signers")...),
	Action: func(c *Context) error {
		return premiumAuthed(c, http.MethodDelete, "/api/requests/"+url.PathEscape(c.String("id")), nil)
	},
}

var premiumExecuteAction = &Command{
	Name:  "execute",
	Usage: "take a quote: sign the confirmation and have the api settle it on chain",
	Flags: []Flag{
		premiumURLFlag(),
		&StringFlag{
			Name:     "quote_id",
			Required: true,
			Usage:    "id of the quote to take, from request-quotes",
		},
		&StringFlag{
			Name:     "nonce",
			Required: true,
			Usage:    "unused confirmation nonce for this signer (decimal uint64)",
		},
		&StringFlag{
			Name:     "private_key",
			Required: true,
			Usage:    "private key of one of the pool's rfq signers",
			EnvVars:  []string{"RYSK_PRIVATE_KEY"},
		},
	},
	Action: premiumExecute,
}

type premiumStoredQuote struct {
	RequestID  string `json:"requestId"`
	Maker      string `json:"maker"`
	Price      string `json:"price"`
	Nonce      string `json:"nonce"`
	Signature  string `json:"signature"`
	ValidUntil int64  `json:"validUntil"`
}

type premiumStoredRequest struct {
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
	PremiumAsset    string `json:"premiumAsset"`
}

// premiumExecute takes a quote by id. The terms come from the api's own copies
// of the quote, its request and the request's pool, so the taker only names
// the quote. It prints the settling transaction's hash; the trade is final once
// that transaction is mined.
func premiumExecute(c *Context) error {
	nonce := c.String("nonce")
	if err := checkNonce(nonce); err != nil {
		return err
	}
	privateKey := c.String("private_key")
	signer, err := newAccountFromPrivateKey(privateKey)
	if err != nil {
		return err
	}

	client := newPremiumClient(c.String("url"))
	var quote premiumStoredQuote
	if err := premiumGet(client, "/api/quotes/"+url.PathEscape(c.String("quote_id")), &quote); err != nil {
		return err
	}
	var request premiumStoredRequest
	if err := premiumGet(client, "/api/requests/"+url.PathEscape(quote.RequestID), &request); err != nil {
		return err
	}
	// the confirmation is signed under the domain the quote was: the pool's
	// option handler
	var pool struct {
		OptionHandler string `json:"optionHandler"`
	}
	if err := premiumGet(client, "/api/pools/"+url.PathEscape(request.Taker), &pool); err != nil {
		return err
	}
	domain, err := premiumQuoteDomain(request.ChainID, &premiumDomainInput{VerifyingContract: pool.OptionHandler})
	if err != nil {
		return err
	}

	// collateralAmount and gasFee are signed but not charged on this path
	confirmation := map[string]any{
		"maker":            quote.Maker,
		"assetAddress":     request.Asset,
		"chainId":          request.ChainID,
		"expiry":           request.Expiry,
		"isPut":            request.IsPut,
		"nonce":            nonce,
		"price":            quote.Price,
		"quantity":         request.Quantity,
		"quoteNonce":       quote.Nonce,
		"quoteSignature":   quote.Signature,
		"strike":           request.Strike,
		"taker":            request.Taker,
		"isTakerBuy":       request.IsTakerBuy,
		"usd":              request.USD,
		"collateralAsset":  request.CollateralAsset,
		"collateralAmount": "0",
		"gasFee":           "0",
	}
	// the typed data encoder wants numbers as strings
	message := make(map[string]any, len(confirmation))
	for k, v := range confirmation {
		message[k] = v
	}
	message["chainId"] = strconv.Itoa(request.ChainID)
	message["expiry"] = strconv.FormatInt(request.Expiry, 10)

	hash, err := confirmationHash(domain, message)
	if err != nil {
		return err
	}
	signature, err := Sign(hash, privateKey)
	if err != nil {
		return err
	}

	// plus the unsigned transport fields: quoteValidUntil exactly as the maker
	// signed it, the request's premiumAsset, and the signer
	confirmation["quoteValidUntil"] = quote.ValidUntil
	confirmation["signature"] = signature
	confirmation["premiumAsset"] = request.PremiumAsset
	confirmation["signer"] = strings.ToLower(signer.Public.Hex())
	body, err := json.Marshal(confirmation)
	if err != nil {
		return fmt.Errorf("invalid payload: %w", err)
	}

	status, res, err := client.do(http.MethodPost, "/api/execute", nil, body, nil)
	if err != nil {
		return err
	}
	// a 400 whose body is not one of the api's own strings is the chain's
	// revert, verbatim
	if err := premiumStatusError(client.baseURL, status, res); err != nil {
		return err
	}
	fmt.Println(strings.TrimSpace(string(res)))
	return nil
}

func premiumGet(client *premiumClient, path string, into any) error {
	status, body, err := client.do(http.MethodGet, path, nil, nil, nil)
	if err != nil {
		return err
	}
	if err := premiumStatusError(client.baseURL, status, body); err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("GET %s: %w (%s)", path, err, body)
	}
	return nil
}
