package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// The same commands run once per stdin line in one process, so a flag set by
// one line must not leak into the next, and a bad line must not stop the rest.
func TestRunCommands(t *testing.T) {
	t.Setenv("RYSK_PRIVATE_KEY", testPrivateKey)
	var sent []JsonRPCRequest
	send = func(payload any) error {
		sent = append(sent, payload.(JsonRPCRequest))
		return nil
	}

	quote := `"quote","--rfq_id","%s","--asset","0x5555555555555555555555555555555555555555","--chain_id","84532","--expiry","1767225600","--maker","0x1111111111111111111111111111111111111111","--nonce","1","--price","1","--quantity","1","--strike","1","--valid_until","1767225600","--usd","0x2222222222222222222222222222222222222222","--collateral","0x2222222222222222222222222222222222222222"`
	lines := []string{
		"[" + strings.Replace(quote, "%s", "first", 1) + `,"--is_put"]`,
		"not json",
		`["quote","--rfq_id","missing-flags"]`,
		`["connect","--url","ws://nowhere"]`, // only the channel commands run here
		"[" + strings.Replace(quote, "%s", "second", 1) + "]",
		`["balances","--account","0xABC"]`,
	}
	out, _ := captureStdout(t, func() error {
		runCommands(strings.NewReader(strings.Join(lines, "\n")))
		printLine([]byte("{\n  \"pretty\": true\n}"))
		return nil
	})

	if len(sent) != 3 {
		t.Fatalf("sent %d payloads, want 3: %+v", len(sent), sent)
	}
	first, second := sent[0].Params.(Quote), sent[1].Params.(Quote)
	if sent[0].ID != "first" || !first.IsPut || first.Signature == "" {
		t.Errorf("first quote = %+v", sent[0])
	}
	if sent[1].ID != "second" || second.IsPut {
		t.Errorf("second quote kept --is_put from the first: %+v", sent[1])
	}
	if sent[2].Method != "balances" || sent[2].Params.(map[string]string)["account"] != "0xabc" {
		t.Errorf("balances = %+v", sent[2])
	}

	// failures come back on stdout as json-rpc errors, under the id the server
	// would have used; every message is one line
	got := strings.Split(strings.TrimSpace(out), "\n")
	want := []string{"missing-flags", "connect"}
	if len(got) != len(want)+1 {
		t.Fatalf("stdout = %q", out)
	}
	for i, id := range want {
		var res JsonRPCResponse
		if err := json.Unmarshal([]byte(got[i]), &res); err != nil || res.ID != id || res.Error == nil || res.Error.Code != -32602 {
			t.Errorf("line %d = %s, want an error for %s", i, got[i], id)
		}
	}
	if got[2] != `{"pretty":true}` {
		t.Errorf("printLine left %q", got[2])
	}
}
