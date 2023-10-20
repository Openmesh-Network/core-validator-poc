package main

import (
	"encoding/binary"
	"fmt"

	"github.com/tendermint/tendermint/abci/example/code"
	"github.com/tendermint/tendermint/abci/server"
	"github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/libs/log"

	tmos "github.com/tendermint/tendermint/libs/os"
)

type Application struct {
	types.BaseApplication

	pendingBlockRewards []types.ValidatorUpdate // Also includes slashing
	hashCount int
	txCount   int
	serial    bool
}

func main() {
	app := NewApplication(true)
	logger, err := log.NewDefaultLogger("text", "debug", true)
	if err != nil {
		fmt.Println(err)
	}

	// Start the listener
	srv, err := server.NewServer("0.0.0.0:26658", "socket", app)
	if err != nil {
		logger.Error("Error while creating server", "err", err)
	}
	srv.SetLogger(logger.With("module", "abci-server"))
	if err := srv.Start(); err != nil {
		logger.Error("Error while setting logger", "err", err)
	}

	// Stop upon receiving SIGTERM or CTRL-C.
	tmos.TrapSignal(logger, func() {
		// Cleanup
		if err := srv.Stop(); err != nil {
			logger.Error("Error while stopping server", "err", err)
		}
	})

	// Run forever.
	select {}
}

func NewApplication(serial bool) *Application {
	return &Application{serial: serial}
}

func (app *Application) Info(req types.RequestInfo) types.ResponseInfo {
	return types.ResponseInfo{Data: fmt.Sprintf("{\"hashes\":%v,\"txs\":%v}", app.hashCount, app.txCount)}
}

func (app *Application) DeliverTx(req types.RequestDeliverTx) types.ResponseDeliverTx {
	if app.serial {
		if len(req.Tx) > 8 {
			return types.ResponseDeliverTx{
				Code: code.CodeTypeEncodingError,
				Log:  fmt.Sprintf("Max tx size is 8 bytes, got %d", len(req.Tx))}
		}
		tx8 := make([]byte, 8)
		copy(tx8[len(tx8)-len(req.Tx):], req.Tx)
		txValue := binary.BigEndian.Uint64(tx8)
		if txValue != uint64(app.txCount) {
			return types.ResponseDeliverTx{
				Code: code.CodeTypeBadNonce,
				Log:  fmt.Sprintf("Invalid nonce. Expected %v, got %v", app.txCount, txValue)}
		}
	}
	app.txCount++
	return types.ResponseDeliverTx{Code: code.CodeTypeOK}
}

func (app *Application) CheckTx(req types.RequestCheckTx) types.ResponseCheckTx {
	if app.serial {
		if len(req.Tx) > 8 {
			return types.ResponseCheckTx{
				Code: code.CodeTypeEncodingError,
				Log:  fmt.Sprintf("Max tx size is 8 bytes, got %d", len(req.Tx))}
		}
		tx8 := make([]byte, 8)
		copy(tx8[len(tx8)-len(req.Tx):], req.Tx)
		txValue := binary.BigEndian.Uint64(tx8)
		if txValue < uint64(app.txCount) {
			return types.ResponseCheckTx{
				Code: code.CodeTypeBadNonce,
				Log:  fmt.Sprintf("Invalid nonce. Expected >= %v, got %v", app.txCount, txValue)}
		}
	}
	return types.ResponseCheckTx{Code: code.CodeTypeOK}
}

func (app *Application) Commit() (resp types.ResponseCommit) {
	app.hashCount++
	if app.txCount == 0 {
		return types.ResponseCommit{}
	}
	hash := make([]byte, 8)
	binary.BigEndian.PutUint64(hash, uint64(app.txCount))
	return types.ResponseCommit{Data: hash}
}

func (app *Application) Query(reqQuery types.RequestQuery) types.ResponseQuery {
	switch reqQuery.Path {
	case "hash":
		return types.ResponseQuery{Value: []byte(fmt.Sprintf("%v", app.hashCount))}
	case "tx":
		return types.ResponseQuery{Value: []byte(fmt.Sprintf("%v", app.txCount))}
	default:
		return types.ResponseQuery{Log: fmt.Sprintf("Invalid query path. Expected hash or tx, got %v", reqQuery.Path)}
	}
}

func (app *Application) BeginBlock(req types.RequestBeginBlock) types.ResponseBeginBlock {
	app.pendingBlockRewards = make([]types.ValidatorUpdate, 0)//len(req.LastCommitInfo.Votes))
	// Scaling factor should not be 2, but 1.00001 or something (should do some calculations on block time for target APY)
	// Code commentend out for now, as tendermint gives me addresses of validators here, but I need their public key to update them
	// I assume these validators are stored somewhere, or alternitavely we need to add and track it in the application storage instead
	// for i := 0; i < len(req.LastCommitInfo.Votes); i++ {
	// 	app.pendingBlockRewards[i] = types.Ed25519ValidatorUpdate(req.LastCommitInfo.Votes[i].Validator.Address, req.LastCommitInfo.Votes[i].Validator.Power*2)
	// }
	return types.ResponseBeginBlock{}
}

func (app *Application) EndBlock(req types.RequestEndBlock) types.ResponseEndBlock {
	return types.ResponseEndBlock{ValidatorUpdates: app.pendingBlockRewards}
}