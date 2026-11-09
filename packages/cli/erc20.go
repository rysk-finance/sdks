package main

import (
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

// erc20ABI is the slice of IERC20 the cli calls.
var erc20ABI, _ = abi.JSON(strings.NewReader(`[
	{"type":"function","name":"approve","stateMutability":"nonpayable","inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[{"name":"","type":"bool"}]},
	{"type":"function","name":"allowance","stateMutability":"view","inputs":[{"name":"owner","type":"address"},{"name":"spender","type":"address"}],"outputs":[{"name":"","type":"uint256"}]}
]`))

func erc20(token common.Address, backend bind.ContractBackend) *bind.BoundContract {
	return bind.NewBoundContract(token, erc20ABI, backend, backend, backend)
}

func erc20Allowance(opts *bind.CallOpts, token *bind.BoundContract, owner, spender common.Address) (*big.Int, error) {
	var out []any
	if err := token.Call(opts, &out, "allowance", owner, spender); err != nil {
		return nil, err
	}
	return out[0].(*big.Int), nil
}
