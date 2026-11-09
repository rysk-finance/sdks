package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestWillRedeemSignsTheCCMessage(t *testing.T) {
	var req JsonRPCRequest
	original := send
	send = func(payload any) error { req = payload.(JsonRPCRequest); return nil }
	t.Cleanup(func() { send = original })

	err := app.Run(context.Background(), []string{"will_redeem",
		"--chain_id", strconv.Itoa(CHAIN_ID_BASE_SEPOLIA),
		"--account", testKeyAddress(t).String(),
		"--otoken", "0x2000000000000000000000000000000000000002",
		"--nonce", "7",
		"--will_redeem",
		"--private_key", testPrivateKey,
	})
	if err != nil {
		t.Fatalf("will_redeem: %v", err)
	}
	params := req.Params.(WillRedeem)
	if req.Method != "will_redeem" || req.ID != "will_redeem" || !params.WillRedeem || params.Nonce != "7" {
		t.Errorf("unexpected request %+v", req)
	}
	hash, err := CreateWillRedeemMessage(params)
	if err != nil {
		t.Fatalf("CreateWillRedeemMessage: %v", err)
	}
	if signer := recoverSigner(t, hash, params.Signature); signer != testKeyAddress(t) {
		t.Errorf("signature recovers to %s, want %s", signer, testKeyAddress(t))
	}
}

func TestSelfRedeemPostsWithAuthHeaders(t *testing.T) {
	var r *http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r = req.Clone(req.Context())
		w.Write([]byte(`{"redeemed":1}`))
	}))
	defer server.Close()

	out, err := captureStdout(t, func() error {
		return app.Run(context.Background(), []string{"self-redeem",
			"--url", server.URL,
			"--chain_id", strconv.Itoa(CHAIN_ID_BASE_SEPOLIA),
			"--nonce", "43",
			"--expiry", "1767225600",
			"--exclude_assets", "0xAB,0xCD",
			"--private_key", testPrivateKey,
		})
	})
	if err != nil {
		t.Fatalf("self-redeem: %v", err)
	}
	if strings.TrimSpace(out) != `{"redeemed":1}` {
		t.Errorf("printed %q", out)
	}
	if r.Method != http.MethodPost || r.URL.Path != "/api/redeem/self" {
		t.Errorf("got %s %s, want POST /api/redeem/self", r.Method, r.URL.Path)
	}
	q := r.URL.Query()
	if q.Get("chainId") != strconv.Itoa(CHAIN_ID_BASE_SEPOLIA) || q.Get("expiry") != "1767225600" || q.Get("excludeAssets") != "0xab,0xcd" {
		t.Errorf("unexpected query %s", r.URL.RawQuery)
	}
	hash, err := EncodeTypedData(premiumAuthTypedData(int64(CHAIN_ID_BASE_SEPOLIA), "43"))
	if err != nil {
		t.Fatalf("EncodeTypedData: %v", err)
	}
	if signer := recoverSigner(t, hash.Bytes(), r.Header.Get("X-Signature")); signer != testKeyAddress(t) {
		t.Errorf("X-Signature recovers to %s, want %s", signer, testKeyAddress(t))
	}
}

func TestSelfRedeemFailsOnServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Self redemption disabled", http.StatusForbidden)
	}))
	defer server.Close()

	_, err := captureStdout(t, func() error {
		return app.Run(context.Background(), []string{"self-redeem",
			"--url", server.URL, "--chain_id", "84532", "--nonce", "1", "--private_key", testPrivateKey})
	})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("got %v, want a 403 error", err)
	}
}
