package main

import (
	b64 "encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/tendermint/tendermint/abci/server"
	"github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/ed25519"
	"github.com/tendermint/tendermint/libs/bytes"
	"github.com/tendermint/tendermint/libs/log"
	tmos "github.com/tendermint/tendermint/libs/os"

	"flag"
	"net/http"

	eth "github.com/ethereum/go-ethereum/crypto"
	"github.com/gorilla/websocket"
)

const (
	CodeTypeOK            					uint32 = 0
	CodeTypeTransactionTypeDecodingError	uint32 = 1
	CodeTypeTransactionDecodingError 		uint32 = 2

	CodeTypeDataNotVerified   				uint32 = 10
	CodeTypeDataOutdated  					uint32 = 11
	CodeTypeDataTooNew  					uint32 = 12

	CodeTypeNotEnoughStakedTokens			uint32 = 20
	CodeTypeNotEnoughUnstakedTokens			uint32 = 21

	CodeTypeDepositNotVerified				uint32 = 30
	CodeTypeDepositInvalidSignature			uint32 = 31

	CodeTypeUnknownError  					uint32 = 999
)

type AbciValidator struct {
	PubKey crypto.PubKey
	GovernancePower int64 // Staked tokens, can be unstaked
	// Tokens has 9 decimals (so * 10^9 to convert to blockchain tokens, / 10*9 to convert to blockchain coins)
	Tokens int64 // Unstaked tokens, can be withdrawn or staked
}

type VerifiedDataItem struct {
	Data string
	Timestamp uint64
}

type Application struct {
	types.BaseApplication

	Validators map[string]AbciValidator // Address -> Validator info
	VerifiedData map[string]VerifiedDataItem // Datafeed -> Data item

	PendingBlockRewards []types.ValidatorUpdate // Also includes slashing
	totalTransactions uint32
}

const (
	TransactionValidateData 	uint8 = 0

	TransactionStakeTokens 		uint8 = 10
	TransactionClaimTokens 		uint8 = 11
	TransactionWithdrawTokens 	uint8 = 12
)

type Transaction struct {
	TransactionType uint8
}

// Try to reach consensus about a piece of data
type ValidateDataTx struct {
	DataFeed string
	DataValue string
	DataTimestamp uint64
}

// Stake / Unstake tokens
type StakeTokensTx struct {
	Amount int64 // Negative amount to unstake
	ValidatorAddress string
	Proof string
}

// Claim tokens by providing ethereum transaction hash, proof is from the ethereum address that deposited their tokens
type ClaimTokensTx struct {
	TransactionHash string
	ValidatorAddress string
	Proof string
}

// Withdraw unstaked tokens to ethreum blockchain
type WithdrawTokensTx struct {
	Amount int64
	Address string
	ValidatorAddress string
	Proof string
}

var verifiedXnodeData = make(map[string]map[uint64]string) // datafeed -> timestamp -> data
var verifiedDeposits = make(map[string]DepositItem) // transaction hash -> deposit info

const (
	XnodeMessageData 	uint8 = 0
	XnodeMessageDeposit uint8 = 1
)

type XnodeMessage struct {
	MessageType uint8
}

type XnodeDataMessage struct {
	DataFeed string
	DataValue string
	DataTimestamp uint64
}

type DepositItem struct {
	Address string
	Amount int64
}

type XnodeDepositMessage struct {
	TransactionHash string
	DepositInfo DepositItem
}

var addr = flag.String("addr", "0.0.0.0:8088", "receiving xnode data")

var upgrader = websocket.Upgrader{} // use default options

var logger log.Logger;

func receiveXnodeData(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Error("Xnode upgrade error", "err", err)
		return
	}
	defer c.Close()
	for {
		_, message, err := c.ReadMessage()
		if err != nil {
			logger.Error("Xnode read error", "err", err)
			break
		}

		xnodeMessage := &XnodeMessage{}
		err = json.Unmarshal(message, xnodeMessage)
		if err != nil {
			logger.Error("Xnode message decode error", "err", err)
		}	

		switch (xnodeMessage.MessageType) {
		case XnodeMessageData:
			xnodeData := &XnodeDataMessage{}
			err = json.Unmarshal(message, xnodeData)
			if err != nil {
				logger.Error("Xnode data message decode error", "err", err)
			}
	
			_, mapExists := verifiedXnodeData[xnodeData.DataFeed]
			if !mapExists {
				verifiedXnodeData[xnodeData.DataFeed] = make(map[uint64]string)
			}
			
			verifiedXnodeData[xnodeData.DataFeed][xnodeData.DataTimestamp] = xnodeData.DataValue
			logger.Info(fmt.Sprintf("Verified %v added: %v at %d", xnodeData.DataFeed, xnodeData.DataValue, xnodeData.DataTimestamp))
		case XnodeMessageDeposit:
			xnodeDeposit := &XnodeDepositMessage{}
			err = json.Unmarshal(message, xnodeDeposit)
			if err != nil {
				logger.Error("Xnode deposit message decode error", "err", err)
			}
			
			verifiedDeposits[xnodeDeposit.TransactionHash] = xnodeDeposit.DepositInfo
			logger.Info(fmt.Sprintf("Verified deposit %v added: (%d from %v)", xnodeDeposit.TransactionHash, xnodeDeposit.DepositInfo.Amount, xnodeDeposit.DepositInfo.Address))
		}

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
	logger, err = log.NewDefaultLogger("text", "debug", true)
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
	http.HandleFunc("/", receiveXnodeData)
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
			logger.Error("Calculated address of public key does not match given address", pk.Address().String(), config.Validators[i].Address)
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

		latestAllowedTimestamp := uint64(time.Now().Unix()) - 1 // Validators should have at least 1 second to receive the data
		if validateDataTx.DataTimestamp >= latestAllowedTimestamp {
			// Is this exploitable? Evil validators accepting transcations that are just under 1 second
			// low latency validators not accepting, higher latency validators do accept (low latency validators get punished?)

			return types.ResponseCheckTx{
				Code: CodeTypeDataTooNew,
				Log:  fmt.Sprintf("New transaction timestamp is not old enough (attempted: %d, latest accepted: %d)", 
					validateDataTx.DataTimestamp, 
					latestAllowedTimestamp,
				),
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
				Code: CodeTypeDataNotVerified,
				Log:  fmt.Sprintf("New transaction data is not confirmed by our xnode (attempted: %v at %d)", 
					validateDataTx.DataValue,
					validateDataTx.DataTimestamp, 
				),
			}
		}

	case TransactionStakeTokens:
		stakeTokensTx := &StakeTokensTx{}
		err := json.Unmarshal(txBytes, stakeTokensTx)
		if err != nil {
			return types.ResponseCheckTx{
				Code: CodeTypeTransactionDecodingError,
				Log:  fmt.Sprint("Not able to parse stake tokens transaction", "err", err),
			}
		}

		validator := app.Validators[stakeTokensTx.ValidatorAddress]
		if stakeTokensTx.Amount > 0 && validator.Tokens - stakeTokensTx.Amount >= 0 {
			return types.ResponseCheckTx{
				Code: CodeTypeNotEnoughUnstakedTokens,
				Log:  fmt.Sprintf("Trying to stake more tokens than unstaked (attempted: %d, unstaked: %d)", stakeTokensTx.Amount, validator.Tokens),
			}
		}
		if stakeTokensTx.Amount < 0 && validator.GovernancePower + stakeTokensTx.Amount >= 0 {
			return types.ResponseCheckTx{
				Code: CodeTypeNotEnoughStakedTokens,
				Log:  fmt.Sprintf("Trying to unstake more tokens than staked (attemped: %d, staked: %d)", -stakeTokensTx.Amount, validator.GovernancePower),
			}
		}
		// check proof

	case TransactionClaimTokens:
		claimTokensTx := &ClaimTokensTx{}
		err := json.Unmarshal(txBytes, claimTokensTx)
		if err != nil {
			return types.ResponseCheckTx{
				Code: CodeTypeTransactionDecodingError,
				Log:  fmt.Sprint("Not able to parse claim tokens transaction", "err", err),
			}
		}

		deposit, exists := verifiedDeposits[claimTokensTx.TransactionHash]
		if !exists {
			// Does this also need a timestamp to check if it's not too recent?
			return types.ResponseCheckTx{
				Code: CodeTypeDepositNotVerified,
				Log:  fmt.Sprintf("Deposit is not confirmed by our xnode (attempted: %v)", claimTokensTx.TransactionHash),
			}
		}

		data := []byte("hello")
		hash := eth.Keccak256Hash(data)
		signerAddress, err := eth.Ecrecover(hash.Bytes(), []byte(claimTokensTx.Proof))
		if err != nil || bytes.HexBytes(signerAddress).String() != deposit.Address {
			return types.ResponseCheckTx{
				Code: CodeTypeDepositInvalidSignature,
				Log:  fmt.Sprintf("Signature does not match depositer address, it should be signed by: %v", deposit.Address),
			}
		}

	case TransactionWithdrawTokens:
		withdrawTokensTx := &WithdrawTokensTx{}
		err := json.Unmarshal(txBytes, withdrawTokensTx)
		if err != nil {
			return types.ResponseCheckTx{
				Code: CodeTypeTransactionDecodingError,
				Log:  fmt.Sprint("Not able to parse withdraw tokens transaction", "err", err),
			}
		}

		validator := app.Validators[withdrawTokensTx.ValidatorAddress]
		if (withdrawTokensTx.Amount > validator.Tokens) {
			return types.ResponseCheckTx{
				Code: CodeTypeNotEnoughUnstakedTokens,
				Log:  fmt.Sprintf("Trying to stake more tokens than unstaked (attempted: %d, unstaked: %d)", withdrawTokensTx.Amount, validator.Tokens),
			}
		}
		// check proof
		
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

	case TransactionStakeTokens:
		stakeTokensTx := &StakeTokensTx{}
		err := json.Unmarshal(txBytes, stakeTokensTx)
		if err != nil {
			return types.ResponseDeliverTx{
				Code: CodeTypeTransactionDecodingError,
				Log:  fmt.Sprint("Not able to parse stake tokens transaction", "err", err),
			}
		}

		validator := app.Validators[stakeTokensTx.ValidatorAddress]

		// Amount can be negative to unstake
		validator.GovernancePower += stakeTokensTx.Amount
		validator.Tokens -= stakeTokensTx.Amount

		app.Validators[stakeTokensTx.ValidatorAddress] = validator

		events = make([]types.Event, 1)
		events[0] = types.Event{Type: "Tokens Staked", Attributes: make([]types.EventAttribute, 2)}
		events[0].Attributes[0] = types.EventAttribute{Key: "validator", Value: fmt.Sprintf("%v", stakeTokensTx.ValidatorAddress)}
		events[0].Attributes[1] = types.EventAttribute{Key: "amount", Value: fmt.Sprintf("%d", stakeTokensTx.Amount)}
		// Do we want to include the proof in here too?

	case TransactionClaimTokens:
		claimTokensTx := &ClaimTokensTx{}
		err := json.Unmarshal(txBytes, claimTokensTx)
		if err != nil {
			return types.ResponseDeliverTx{
				Code: CodeTypeTransactionDecodingError,
				Log:  fmt.Sprint("Not able to parse claim tokens transaction", "err", err),
			}
		}

		validator := app.Validators[claimTokensTx.ValidatorAddress]

		deposit := verifiedDeposits[claimTokensTx.TransactionHash]
		validator.Tokens += deposit.Amount
		delete(verifiedDeposits, claimTokensTx.TransactionHash) // Prevent deposit from being claimed again

		app.Validators[claimTokensTx.ValidatorAddress] = validator

		events = make([]types.Event, 1)
		events[0] = types.Event{Type: "Token Claimed", Attributes: make([]types.EventAttribute, 2)}
		events[0].Attributes[0] = types.EventAttribute{Key: "validator", Value: fmt.Sprintf("%v", claimTokensTx.ValidatorAddress)}
		events[0].Attributes[1] = types.EventAttribute{Key: "transactionhash", Value: fmt.Sprintf("%v", claimTokensTx.TransactionHash)}
		// Do we want to include the proof in here too?
		// Do we want to inlcude deposit info (you can check that on Ethereum with transaction hash tho)

	case TransactionWithdrawTokens:
		withdrawTokensTx := &WithdrawTokensTx{}
		err := json.Unmarshal(txBytes, withdrawTokensTx)
		if err != nil {
			return types.ResponseDeliverTx{
				Code: CodeTypeTransactionDecodingError,
				Log:  fmt.Sprint("Not able to parse withdraw tokens transaction", "err", err),
			}
		}

		validator := app.Validators[withdrawTokensTx.ValidatorAddress]

		validator.Tokens -= withdrawTokensTx.Amount

		app.Validators[withdrawTokensTx.ValidatorAddress] = validator

		events = make([]types.Event, 1)
		events[0] = types.Event{Type: "Tokens Withdrawn", Attributes: make([]types.EventAttribute, 2)}
		events[0].Attributes[0] = types.EventAttribute{Key: "validator", Value: fmt.Sprintf("%v", withdrawTokensTx.ValidatorAddress)}
		events[0].Attributes[1] = types.EventAttribute{Key: "amount", Value: fmt.Sprintf("%d", withdrawTokensTx.Amount)}
		// Do we want to include the proof in here too?

	}

	return types.ResponseDeliverTx{Code: CodeTypeOK, Events: events}
}

func (app *Application) DeliverTx(req types.RequestDeliverTx) types.ResponseDeliverTx {
	// Not sure if this check if required or if the tendermint node handles this for us
	// In the counter example application the check is also in here
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

		validator.GovernancePower += validator.GovernancePower / 10000
		app.PendingBlockRewards[i] = types.Ed25519ValidatorUpdate(validator.PubKey.Bytes(), validator.GovernancePower)

		app.Validators[address] = validator
	}
	for i := 0; i < len(req.ByzantineValidators); i++ {
		address := bytes.HexBytes(req.ByzantineValidators[i].Validator.Address).String()
		validator := app.Validators[address]

		// Can a validator withdraw their governance power before the evidence against their actions is finalized to prevent punishment?
		validator.GovernancePower -= validator.GovernancePower / 100
		if (validator.GovernancePower < 10_000 * 10^9) {
			// If their GovernancePower is bellow the threshold, move all to tokens and give them GovernancePower 0
			validator.Tokens += validator.GovernancePower;
			validator.GovernancePower = 0;
		}
		app.PendingBlockRewards[len(req.LastCommitInfo.Votes)+i] = types.Ed25519ValidatorUpdate(validator.PubKey.Bytes(), validator.GovernancePower)

		app.Validators[address] = validator
	}
	return types.ResponseBeginBlock{}
}

func (app *Application) EndBlock(req types.RequestEndBlock) types.ResponseEndBlock {
	return types.ResponseEndBlock{ValidatorUpdates: app.PendingBlockRewards}
}