package main

import (
	b64 "encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/tendermint/tendermint/abci/server"
	"github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/ed25519"
	"github.com/tendermint/tendermint/libs/bytes"
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

type AbciValidator struct {
	PubKey crypto.PubKey
	GovernancePower int64 // Staked tokens, can be unstaked
	Tokens int64 // Free tokens, can be withdrawn or staked
}

type Application struct {
	types.BaseApplication

	validators map[string]AbciValidator
	pendingBlockRewards []types.ValidatorUpdate // Also includes slashing
	binanceBTCUSDT uint32
	binanceBTCUSDTTimestamp uint64

	totalTransactions uint32
}
var binanceBTCUSDTAtTimestamp = make(map[uint64]uint32) // Verfied data from our xnode

var addr = flag.String("addr", "0.0.0.0:8088", "receiving xnode data")

var upgrader = websocket.Upgrader{} // use default options

func receiveBinanceData(w http.ResponseWriter, r *http.Request) {
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

type ConfigFile struct {
    Validators []ConfigValidator `json:"validators"`
}

type ConfigValidator struct {
    Address string `json:"address"`
	PubKey ConfigPubKey `json:"pub_key"`
	Power string `json:"power"`
}

type ConfigPubKey struct {
	Type string `json:"type"`
	Value string `json:"value"`
}

func main() {
	// Needed for the intial validators
	configFile := "/tendermint/" + os.Args[1]
	configFileContent, err := os.ReadFile(configFile)
	if err != nil {
		fmt.Println("Error while reading config file", "err", err)
	}

	config := &ConfigFile{}
    err = json.Unmarshal(configFileContent, config)
	if err != nil {
		fmt.Println("Error while parsing config file json", "err", err)
	}

	app := NewApplication(*config)
	logger, err := log.NewDefaultLogger("text", "debug", true)
	if err != nil {
		fmt.Println("Error while creating logger", "err", err)
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
	http.HandleFunc("/", receiveBinanceData)
	fmt.Println(http.ListenAndServe(*addr, nil))

	// Run forever.
	select {}
}

func NewApplication(config ConfigFile) *Application {
	validators := make(map[string]AbciValidator)
	for i := 0; i < len(config.Validators); i++ {
		powerAsInt, err := strconv.ParseInt(config.Validators[i].Power, 10, 64)
		if err != nil {
			panic(err)
		}
		
		pkBytes, err := b64.StdEncoding.DecodeString(config.Validators[i].PubKey.Value)
		if err != nil {
			panic(err)
		}
		pk := ed25519.PubKey(pkBytes)
		if pk.Address().String() != config.Validators[i].Address {
			fmt.Println("Calculated address of public key does not match given address", pk.Address().String(), config.Validators[i].Address)
		}
		validators[config.Validators[i].Address] = AbciValidator{
			PubKey: pk,
			GovernancePower: powerAsInt,
			Tokens: 0,
		}
	}
	return &Application{validators: validators}
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
	// This assumes all punished validators are still validating though!
	app.pendingBlockRewards = make([]types.ValidatorUpdate, len(req.LastCommitInfo.Votes) + len(req.ByzantineValidators))
	for i := 0; i < len(req.LastCommitInfo.Votes); i++ {
		address := bytes.HexBytes(req.LastCommitInfo.Votes[i].Validator.Address).String()
		validator := app.validators[address]

		// This should be changed to be dependant on your stake
		validator.GovernancePower += 1
		app.pendingBlockRewards[i] = types.Ed25519ValidatorUpdate(validator.PubKey.Bytes(), validator.GovernancePower)

		app.validators[address] = validator
	}
	for i := 0; i < len(req.ByzantineValidators); i++ {
		address := bytes.HexBytes(req.ByzantineValidators[i].Validator.Address).String()
		validator := app.validators[address]

		// Check if it's not going bellow 0
		// Can a validator withdrawl their governance power before the evidence against their actions is finalized to prevent punishment?
		// This should be changed to be dependant on your stake
		validator.GovernancePower -= 1
		// If their GovernancePower is bellow the threshold, move all to tokens and give them GovernancePower 0
		app.pendingBlockRewards[len(req.LastCommitInfo.Votes)+i] = types.Ed25519ValidatorUpdate(validator.PubKey.Bytes(), validator.GovernancePower)

		app.validators[address] = validator
	}
	return types.ResponseBeginBlock{}
}

func (app *Application) EndBlock(req types.RequestEndBlock) types.ResponseEndBlock {
	return types.ResponseEndBlock{ValidatorUpdates: app.pendingBlockRewards}
}