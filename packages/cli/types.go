package main

type Transfer struct {
	User      string `json:"user"`
	Asset     string `json:"asset"`
	ChainID   int    `json:"chainId"`
	Amount    string `json:"amount"`
	IsDeposit bool   `json:"isDeposit"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
}

type Quote struct {
	AssetAddress    string `json:"assetAddress"`
	ChainID         int    `json:"chainId"`
	Expiry          int64  `json:"expiry"`
	IsPut           bool   `json:"isPut"`
	IsTakerBuy      bool   `json:"isTakerBuy"`
	Maker           string `json:"maker"`
	Nonce           string `json:"nonce"`
	Price           string `json:"price"`
	Quantity        string `json:"quantity"`
	Strike          string `json:"strike"`
	Signature       string `json:"signature"`
	ValidUntil      int64  `json:"validUntil"`
	USD             string `json:"usd"`
	CollateralAsset string `json:"collateralAsset"`
	// PremiumAsset is not part of the signed Quote type, so it is dropped
	// before hashing in CreateQuoteMessage.
	PremiumAsset string `json:"premiumAsset,omitempty"`
}

type WillRedeem struct {
	Account    string `json:"account"`
	Otoken     string `json:"otoken"`
	ChainID    int    `json:"chainId"`
	WillRedeem bool   `json:"willRedeem"`
	Nonce      string `json:"nonce"`
	Signature  string `json:"signature"`
}
