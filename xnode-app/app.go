package main

import (
	"encoding/binary"
	"fmt"

	"github.com/tendermint/tendermint/abci/server"
	"github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/libs/log"
	tmos "github.com/tendermint/tendermint/libs/os"

	"flag"
	"net/http"

	"github.com/gorilla/websocket"
)

const (
	CodeTypeOK            uint32 = 0
	CodeTypeEncodingError uint32 = 1
	CodeTypeDataInvalid   uint32 = 2
	CodeTypeDataOutdated  uint32 = 3
	CodeTypeUnknownError  uint32 = 4
)

type Application struct {
	types.BaseApplication

	pendingBlockRewards []types.ValidatorUpdate // Also includes slashing
	binanceBTCUSDT uint32
	binanceBTCUSDTTimestamp uint64

	totalTransactions uint32
}
var binanceBTCUSDTAtTimestamp = make(map[uint64]uint32) // Verfied data from our xnode

var addr = flag.String("addr", "0.0.0.0:8088", "receiving xnode data")

var upgrader = websocket.Upgrader{} // use default options

func (app *Application) receiveBinanceData(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Print("upgrade:", err)
		return
	}
	defer c.Close()
	for {
		_, message, err := c.ReadMessage()
		if err != nil {
			fmt.Println("read:", err)
			break
		}
		binanceBTCUSDTBytes := make([]byte, 4)
		binanceBTCUSDTTimestampBytes := make([]byte, 8)
		copy(binanceBTCUSDTBytes, message[0:4])
		copy(binanceBTCUSDTTimestampBytes, message[4:12])
		verifiedBinanceBTCUSDT := binary.BigEndian.Uint32(binanceBTCUSDTBytes)
		verifiedBinanceBTCUSDTTimestamp := binary.BigEndian.Uint64(binanceBTCUSDTTimestampBytes)
		binanceBTCUSDTAtTimestamp[verifiedBinanceBTCUSDTTimestamp] = verifiedBinanceBTCUSDT
		fmt.Println("Verified price added:", verifiedBinanceBTCUSDT, "at", verifiedBinanceBTCUSDTTimestamp)
	}
}

func main() {
	app := NewApplication()
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

	flag.Parse()
	http.HandleFunc("/", app.receiveBinanceData)
	fmt.Println(http.ListenAndServe(*addr, nil))

	// Run forever.
	select {}
}

func NewApplication() *Application {
	return &Application{}
}

func (app *Application) Info(req types.RequestInfo) types.ResponseInfo {
	return types.ResponseInfo{Data: fmt.Sprintf("{\"BTCUSDT\":%v,\"lastUpdate\":%v,\"totalUpdates\":%v}", app.binanceBTCUSDT, app.binanceBTCUSDTTimestamp, app.totalTransactions)}
}

func (app *Application) VerifyData(tx []byte) (uint32, uint64, uint32) {
	binanceBTCUSDTBytes := make([]byte, 4)
	binanceBTCUSDTTimestampBytes := make([]byte, 8)
	copy(binanceBTCUSDTBytes, tx[0:4])
	copy(binanceBTCUSDTTimestampBytes, tx[4:12])
	reqBinanceBTCUSDT := binary.BigEndian.Uint32(binanceBTCUSDTBytes)
	reqBinanceBTCUSDTTimestamp := binary.BigEndian.Uint64(binanceBTCUSDTTimestampBytes)
	if reqBinanceBTCUSDTTimestamp <= app.binanceBTCUSDTTimestamp {
		return 0, 0, CodeTypeDataOutdated
	}
	if (binanceBTCUSDTAtTimestamp[reqBinanceBTCUSDTTimestamp] != reqBinanceBTCUSDT) {
		fmt.Println(binanceBTCUSDTAtTimestamp[reqBinanceBTCUSDTTimestamp], "vs", reqBinanceBTCUSDT, "at", reqBinanceBTCUSDTTimestamp)
		return 0, 0, CodeTypeDataInvalid
	}
	return reqBinanceBTCUSDT, reqBinanceBTCUSDTTimestamp, 0
}

func (app *Application) DeliverTx(req types.RequestDeliverTx) types.ResponseDeliverTx {
	if len(req.Tx) != 12 {
		return types.ResponseDeliverTx{
			Code: CodeTypeEncodingError,
			Log:  fmt.Sprintf("Expected tx size is 12 bytes, got %d", len(req.Tx)),
		}
	}
	price, time, responseCode := app.VerifyData(req.Tx)
	if responseCode != 0 {
		return types.ResponseDeliverTx{
			Code: responseCode,
			Log:  "Data verification returned false",
		}
	}
	app.binanceBTCUSDT = price
	app.binanceBTCUSDTTimestamp = time
	app.totalTransactions++
	events := make([]types.Event, 1)
	events[0] = types.Event{Type: "binanceUSDT", Attributes: make([]types.EventAttribute, 2)}
	events[0].Attributes[0] = types.EventAttribute{Key: "price", Value: fmt.Sprintf("%v", app.binanceBTCUSDT)}
	events[0].Attributes[1] = types.EventAttribute{Key: "timestamp", Value: fmt.Sprintf("%v", app.binanceBTCUSDTTimestamp)}
	return types.ResponseDeliverTx{Code: CodeTypeOK, Events: events}
}

func (app *Application) CheckTx(req types.RequestCheckTx) types.ResponseCheckTx {
	if len(req.Tx) != 12 {
		return types.ResponseCheckTx{
			Code: CodeTypeEncodingError,
			Log:  fmt.Sprintf("Expected tx size is 12 bytes, got %d", len(req.Tx)),
		}
	}
	_, _, responseCode := app.VerifyData(req.Tx)
	if responseCode != 0 {
		return types.ResponseCheckTx{
			Code: responseCode,
			Log:  "Data verification returned false",
		}
	}
	return types.ResponseCheckTx{Code: CodeTypeOK}
}

func (app *Application) Commit() (resp types.ResponseCommit) {
	if app.totalTransactions == 0 {
		return types.ResponseCommit{}
	}
	hash := make([]byte, 8)
	binary.BigEndian.PutUint64(hash, uint64(app.totalTransactions))
	return types.ResponseCommit{Data: hash}
}

func (app *Application) Query(reqQuery types.RequestQuery) types.ResponseQuery {
	switch reqQuery.Path {
	case "price":
		return types.ResponseQuery{Value: []byte(fmt.Sprintf("%v", app.binanceBTCUSDT))}
	case "time":
		return types.ResponseQuery{Value: []byte(fmt.Sprintf("%v", app.binanceBTCUSDTTimestamp))}
	case "tx":
		return types.ResponseQuery{Value: []byte(fmt.Sprintf("%v", app.totalTransactions))}
	default:
		return types.ResponseQuery{Log: fmt.Sprintf("Invalid query path. Expected price, time or tx, got %v", reqQuery.Path)}
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