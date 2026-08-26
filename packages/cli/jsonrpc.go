package main

type JsonRPCRequest struct {
	JsonRPC string `json:"jsonrpc" binding:"required"`
	ID      string `json:"id" binding:"required"`
	Method  string `json:"method" binding:"required"`
	Params  any    `json:"params"`
}

type ErrorData struct {
	Code    int    `json:"code,omitempty" binding:"required"`
	Message string `json:"message,omitempty" binding:"required"`
	Data    any    `json:"data,omitempty"`
}

type JsonRPCResponse struct {
	JsonRPC string     `json:"jsonrpc" binding:"required"`
	ID      string     `json:"id" binding:"required"`
	Result  any        `json:"result,omitempty"`
	Error   *ErrorData `json:"error,omitempty"`
}
