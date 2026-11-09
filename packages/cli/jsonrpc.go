package main

type JsonRPCRequest struct {
	JsonRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type ErrorData struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type JsonRPCResponse struct {
	JsonRPC string     `json:"jsonrpc"`
	ID      string     `json:"id"`
	Result  any        `json:"result,omitempty"`
	Error   *ErrorData `json:"error,omitempty"`
}
