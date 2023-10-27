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
	CodeTypeOK            					uint32 = 0
	CodeTypeTransactionTypeDecodingError	uint32 = 1
	CodeTypeTransactionDecodingError 		uint32 = 2

	CodeTypeDataInvalid   					uint32 = 10
	CodeTypeDataOutdated  					uint32 = 11

	CodeTypeUnknownError  					uint32 = 999
)

type AbciValidator struct {
	PubKey crypto.PubKey
	GovernancePower int64 // Staked tokens, can be unstaked
	Tokens int64 // Unstaked tokens, can be withdrawn or staked
}

type VerifiedDataItem struct {
	Data string
	Timestamp uint64
}

type Application struct {
	types.BaseApplication

	Validators map[string]AbciValidator
	VerifiedData map[string]VerifiedDataItem

	PendingBlockRewards []types.ValidatorUpdate // Also includes slashing
	totalTransactions uint32
}

const (
	TransactionValidateData 	uint8 = 0
	TransactionStakeTokens 		uint8 = 1
	TransactionClaimTokens 		uint8 = 2
	TransactionWithdrawTokens 	uint8 = 3
)

type Transaction struct {
	TransactionType uint8
}

type ValidateDataTx struct {
	DataFeed string
	DataValue string
	DataTimestamp uint64
}

type StakeTokensTx struct {
}

type ClaimTokensTx struct {
}

type WithdrawTokensTx struct {
}

var verifiedXnodeData = make(map[string]map[uint64]string)

type XnodeDataMessage struct {
	DataFeed string
	DataValue string
	DataTimestamp uint64
}

var addr = flag.String("addr", "0.0.0.0:8088", "receiving xnode data")

var upgrader = websocket.Upgrader{} // use default options

func receiveBinanceData(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Print("Xnode upgrade error", "err", err)
		return
	}
	defer c.Close()
	for {
		_, message, err := c.ReadMessage()
		if err != nil {
			fmt.Println("Xnode read error", "err", err)
			break
		}
		xnodeData := &XnodeDataMessage{}
		err = json.Unmarshal(message, xnodeData)
		if err != nil {
			fmt.Println("Xnode message decode error", "err", err)
		}

		_, mapExists := verifiedXnodeData[xnodeData.DataFeed]
		if !mapExists {
			verifiedXnodeData[xnodeData.DataFeed] = make(map[uint64]string)
		}
		
		verifiedXnodeData[xnodeData.DataFeed][xnodeData.DataTimestamp] = xnodeData.DataValue
		fmt.Printf("Verified %v added: %v at %d", xnodeData.DataFeed, xnodeData.DataValue, xnodeData.DataTimestamp)
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
	configFile := "/tendermint/" + os.Args[1] // /tendermint is a volume from Docker refering to ../tendermint/build
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
	return &Application{Validators: validators, VerifiedData: make(map[string]VerifiedDataItem)}
}

func (app *Application) Info(req types.RequestInfo) types.ResponseInfo {
	verifiedData, err := json.Marshal(app.VerifiedData)
	if err != nil {
		return types.ResponseInfo{
			Data: fmt.Sprintf("Something went wrong parsing verified data err %v", err),
		}
	}

	validators, err := json.Marshal(app.Validators)
	if err != nil {
		return types.ResponseInfo{
			Data: fmt.Sprintf("Something went wrong parsing validators err %v", err),
		}
	}

	return types.ResponseInfo{Data: fmt.Sprintf("{\"VerifiedData\":%v,\"Validators\":%v,\"TotalUpdates\":%v}", string(verifiedData), string(validators), app.totalTransactions)}
}

func (app *Application) ValidateTx(txBytes []byte) types.ResponseCheckTx {
	tx := &Transaction{}
	err := json.Unmarshal(txBytes, tx)
	if err != nil {
		return types.ResponseCheckTx{
			Code: CodeTypeTransactionTypeDecodingError,
			Log:  fmt.Sprint("Not able to parse transaction type", "err", err),
		}
	}

	switch (tx.TransactionType) {
	case TransactionValidateData:
		validateDataTx := &ValidateDataTx{}
		err := json.Unmarshal(txBytes, validateDataTx)
		if err != nil {
			return types.ResponseCheckTx{
				Code: CodeTypeTransactionDecodingError,
				Log:  fmt.Sprint("Not able to parse validate data transaction", "err", err),
			}
		}

		if validateDataTx.DataTimestamp <= app.VerifiedData[validateDataTx.DataFeed].Timestamp {
			return types.ResponseCheckTx{
				Code: CodeTypeDataOutdated,
				Log:  fmt.Sprintf("New transaction timestamp is not newer than latest one (attempted: %d, latest: %d)", 
					validateDataTx.DataTimestamp, 
					app.VerifiedData[validateDataTx.DataFeed].Timestamp,
				),
			}
		}
		dataFromXnode, exists := verifiedXnodeData[validateDataTx.DataFeed][validateDataTx.DataTimestamp]
		if (!exists || dataFromXnode != validateDataTx.DataValue) {
			return types.ResponseCheckTx{
				Code: CodeTypeDataInvalid,
				Log:  fmt.Sprintf("New transaction data is not confirmed by our xnode (attempted: %v at %d)", 
					validateDataTx.DataValue,
					validateDataTx.DataTimestamp, 
				),
			}
		}
	}

	return types.ResponseCheckTx{Code: CodeTypeOK}
}

func (app *Application) ExecuteTx(txBytes []byte) types.ResponseDeliverTx {
	tx := &Transaction{}
	err := json.Unmarshal(txBytes, tx)
	if err != nil {
		return types.ResponseDeliverTx{
			Code: CodeTypeTransactionTypeDecodingError,
			Log:  fmt.Sprint("Not able to parse transaction type", "err", err),
		}
	}

	events := make([]types.Event, 0)
	switch (tx.TransactionType) {
	case TransactionValidateData:
		validateDataTx := &ValidateDataTx{}
		err := json.Unmarshal(txBytes, validateDataTx)
		if err != nil {
			return types.ResponseDeliverTx{
				Code: CodeTypeTransactionDecodingError,
				Log:  fmt.Sprint("Not able to parse validate data transaction", "err", err),
			}
		}

		
		app.VerifiedData[validateDataTx.DataFeed] = VerifiedDataItem{Data: validateDataTx.DataValue, Timestamp: validateDataTx.DataTimestamp}
		events = make([]types.Event, 1)
		events[0] = types.Event{Type: "Data Verified", Attributes: make([]types.EventAttribute, 3)}
		events[0].Attributes[0] = types.EventAttribute{Key: "feed", Value: fmt.Sprintf("%v", validateDataTx.DataFeed)}
		events[0].Attributes[1] = types.EventAttribute{Key: "data", Value: fmt.Sprintf("%v", validateDataTx.DataValue)}
		events[0].Attributes[2] = types.EventAttribute{Key: "timestamp", Value: fmt.Sprintf("%d", validateDataTx.DataTimestamp)}
	}

	return types.ResponseDeliverTx{Code: CodeTypeOK, Events: events}
}

func (app *Application) DeliverTx(req types.RequestDeliverTx) types.ResponseDeliverTx {
	check := app.ValidateTx(req.Tx)
	if (check.Code != 0) {
		return types.ResponseDeliverTx{
			Code: check.Code,
			Log: check.Log,
		}
	}

	app.totalTransactions++
	return app.ExecuteTx(req.Tx)
}

func (app *Application) CheckTx(req types.RequestCheckTx) types.ResponseCheckTx {
	return app.ValidateTx(req.Tx)
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
	case "tx":
		return types.ResponseQuery{Value: []byte(fmt.Sprintf("%v", app.totalTransactions))}
	default:
		return types.ResponseQuery{Log: fmt.Sprintf("Invalid query path. Expected price, time or tx, got %v", reqQuery.Path)}
	}
}

func (app *Application) BeginBlock(req types.RequestBeginBlock) types.ResponseBeginBlock {
	// This assumes all punished validators are still validating though!
	app.PendingBlockRewards = make([]types.ValidatorUpdate, len(req.LastCommitInfo.Votes) + len(req.ByzantineValidators))
	for i := 0; i < len(req.LastCommitInfo.Votes); i++ {
		address := bytes.HexBytes(req.LastCommitInfo.Votes[i].Validator.Address).String()
		validator := app.Validators[address]

		// This should be changed to be dependant on your stake
		validator.GovernancePower += 1
		app.PendingBlockRewards[i] = types.Ed25519ValidatorUpdate(validator.PubKey.Bytes(), validator.GovernancePower)

		app.Validators[address] = validator
	}
	for i := 0; i < len(req.ByzantineValidators); i++ {
		address := bytes.HexBytes(req.ByzantineValidators[i].Validator.Address).String()
		validator := app.Validators[address]

		// Check if it's not going bellow 0
		// Can a validator withdrawl their governance power before the evidence against their actions is finalized to prevent punishment?
		// This should be changed to be dependant on your stake
		validator.GovernancePower -= 1
		// If their GovernancePower is bellow the threshold, move all to tokens and give them GovernancePower 0
		app.PendingBlockRewards[len(req.LastCommitInfo.Votes)+i] = types.Ed25519ValidatorUpdate(validator.PubKey.Bytes(), validator.GovernancePower)

		app.Validators[address] = validator
	}
	return types.ResponseBeginBlock{}
}

func (app *Application) EndBlock(req types.RequestEndBlock) types.ResponseEndBlock {
	return types.ResponseEndBlock{ValidatorUpdates: app.PendingBlockRewards}
}