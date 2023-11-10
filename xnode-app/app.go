package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/cometbft/cometbft/abci/types"
	cfg "github.com/cometbft/cometbft/config"
	"github.com/cometbft/cometbft/crypto"
	"github.com/cometbft/cometbft/crypto/ed25519"
	"github.com/cometbft/cometbft/libs/bytes"
	cmtflags "github.com/cometbft/cometbft/libs/cli/flags"
	cmtlog "github.com/cometbft/cometbft/libs/log"
	cmtos "github.com/cometbft/cometbft/libs/os"
	nm "github.com/cometbft/cometbft/node"
	"github.com/cometbft/cometbft/p2p"
	"github.com/cometbft/cometbft/privval"
	"github.com/cometbft/cometbft/proxy"
	"github.com/spf13/viper"

	"net/http"

	eth "github.com/ethereum/go-ethereum/crypto"
	"github.com/gorilla/websocket"
)

const (
	CodeTypeOK                           uint32 = 0
	CodeTypeTransactionTypeDecodingError uint32 = 1
	CodeTypeTransactionDecodingError     uint32 = 2

	CodeTypeDataNotVerified uint32 = 10
	CodeTypeDataOutdated    uint32 = 11
	CodeTypeDataTooNew      uint32 = 12

	CodeTypeNotEnoughStakedTokens   uint32 = 20
	CodeTypeNotEnoughUnstakedTokens uint32 = 21
	CodeTypeInvalidSingature        uint32 = 22

	CodeTypeDepositNotVerified      uint32 = 30
	CodeTypeDepositInvalidSignature uint32 = 31

	CodeTypeUnknownError uint32 = 999
)

type AbciValidator struct {
	PubKey          crypto.PubKey
	GovernancePower int64 // Staked tokens, can be unstaked
	// Tokens has 9 decimals (so * 10^9 to convert to blockchain tokens, / 10*9 to convert to blockchain coins)
	Tokens int64  // Unstaked tokens, can be withdrawn or staked
	Nonce  uint32 // To prevent replay attacks
}

type VerifiedDataItem struct {
	Data      string
	Timestamp uint64
}

type Application struct {
	types.BaseApplication

	Validators   map[string]AbciValidator    // Address -> Validator info
	VerifiedData map[string]VerifiedDataItem // Datafeed -> Data item

	TotalTransactions uint32
}

// Transactions
const (
	TransactionValidateData uint8 = 0

	TransactionStakeTokens    uint8 = 10
	TransactionClaimTokens    uint8 = 11
	TransactionWithdrawTokens uint8 = 12
)

type Transaction struct {
	TransactionType uint8
}

// Try to reach consensus about a piece of data
type ValidateDataTx struct {
	DataFeed      string
	DataValue     string
	DataTimestamp uint64
}

// Stake / Unstake tokens
type StakeTokensTx struct {
	Amount           int64 // Negative amount to unstake
	ValidatorAddress string
	Proof            string // "Stake" + Amount (hex) + Nonce (hex)
}

// Claim tokens by providing ethereum transaction hash, proof is from the ethereum address that deposited their tokens
type ClaimTokensTx struct {
	TransactionHash  string
	ValidatorAddress string
	Proof            string
}

// Withdraw unstaked tokens to ethreum blockchain
type WithdrawTokensTx struct {
	Amount           int64
	Address          string
	ValidatorAddress string
	Proof            string // "Withdraw" + Amount (hex) + Address + None (hex)
}

// Xnode
var verifiedXnodeData = make(map[string]map[uint64]string) // datafeed -> timestamp -> data
var verifiedDeposits = make(map[string]DepositItem)        // transaction hash -> deposit info

const (
	XnodeMessageData    uint8 = 0
	XnodeMessageDeposit uint8 = 1
)

type XnodeMessage struct {
	MessageType uint8
}

type XnodeDataMessage struct {
	DataFeed      string
	DataValue     string
	DataTimestamp uint64
}

type DepositItem struct {
	Address string
	Amount  int64
}

type XnodeDepositMessage struct {
	TransactionHash string
	DepositInfo     DepositItem
}

func receiveXnodeData(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal("Xnode upgrade error", "err", err)
		return
	}
	defer c.Close()
	for {
		_, message, err := c.ReadMessage()
		if err != nil {
			log.Fatal("Xnode read error", "err", err)
			break
		}

		xnodeMessage := &XnodeMessage{}
		err = json.Unmarshal(message, xnodeMessage)
		if err != nil {
			log.Fatal("Xnode message decode error", "err", err)
		}

		switch xnodeMessage.MessageType {
		case XnodeMessageData:
			xnodeData := &XnodeDataMessage{}
			err = json.Unmarshal(message, xnodeData)
			if err != nil {
				log.Fatal("Xnode data message decode error", "err", err)
			}

			_, mapExists := verifiedXnodeData[xnodeData.DataFeed]
			if !mapExists {
				verifiedXnodeData[xnodeData.DataFeed] = make(map[uint64]string)
			}

			verifiedXnodeData[xnodeData.DataFeed][xnodeData.DataTimestamp] = xnodeData.DataValue
			log.Printf("Verified %v added: %v at %d", xnodeData.DataFeed, xnodeData.DataValue, xnodeData.DataTimestamp)
		case XnodeMessageDeposit:
			xnodeDeposit := &XnodeDepositMessage{}
			err = json.Unmarshal(message, xnodeDeposit)
			if err != nil {
				log.Fatal("Xnode deposit message decode error", "err", err)
			}

			verifiedDeposits[xnodeDeposit.TransactionHash] = xnodeDeposit.DepositInfo
			log.Printf("Verified deposit %v added: (%d from %v)", xnodeDeposit.TransactionHash, xnodeDeposit.DepositInfo.Amount, xnodeDeposit.DepositInfo.Address)
		}

	}
}

var upgrader = websocket.Upgrader{} // use default options
var addr = flag.String("addr", "0.0.0.0:8088", "Address for websocket receiving xnode data")
var homeDir = flag.String("cmt-home", "", "Path to the CometBFT config directory (if empty, uses $HOME/.cometbft)")

const (
	minimumValidatorPower = 10_000*10 ^ 9
)

func main() {
	flag.Parse()
	if *homeDir == "" {
		*homeDir = os.ExpandEnv("$HOME/.cometbft")
	}

	config := cfg.DefaultConfig()
	config.SetRoot(*homeDir)
	viper.SetConfigFile(fmt.Sprintf("%s/%s", *homeDir, "config/config.toml"))

	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Reading config: %v", err)
	}
	if err := viper.Unmarshal(config); err != nil {
		log.Fatalf("Decoding config: %v", err)
	}
	if err := config.ValidateBasic(); err != nil {
		log.Fatalf("Invalid configuration data: %v", err)
	}
	// dbPath := filepath.Join(*homeDir, "badger")
	// db, err := badger.Open(badger.DefaultOptions(dbPath))

	// if err != nil {
	//     log.Fatalf("Opening database: %v", err)
	// }
	// defer func() {
	//     if err := db.Close(); err != nil {
	//         log.Printf("Closing database: %v", err)
	//     }
	// }()

	// app := NewApplication(db)
	app := NewApplication()

	pv := privval.LoadFilePV(
		config.PrivValidatorKeyFile(),
		config.PrivValidatorStateFile(),
	)

	nodeKey, err := p2p.LoadNodeKey(config.NodeKeyFile())
	if err != nil {
		log.Fatalf("failed to load node's key: %v", err)
	}

	logger := cmtlog.NewTMLogger(cmtlog.NewSyncWriter(os.Stdout))
	logger, err = cmtflags.ParseLogLevel(config.LogLevel, logger, cfg.DefaultLogLevel)

	if err != nil {
		log.Fatalf("failed to parse log level: %v", err)
	}

	// Consensus node
	node, err := nm.NewNode(
		config,
		pv,
		nodeKey,
		proxy.NewLocalClientCreator(app),
		nm.DefaultGenesisDocProviderFunc(config),
		cfg.DefaultDBProvider,
		nm.DefaultMetricsProvider(config.Instrumentation),
		logger,
	)

	if err != nil {
		log.Fatalf("Creating node: %v", err)
	}

	if err := node.Start(); err != nil {
		log.Fatalf("failed to start node: %v", err)
	}
	logger.Info("Started node", "nodeInfo", node.Switch().NodeInfo())

	// Stop upon receiving SIGTERM or CTRL-C.
	cmtos.TrapSignal(logger, func() {
		if node.IsRunning() {
			if err := node.Stop(); err != nil {
				log.Fatal("unable to stop the node", "error", err)
			}
		}
	})

	// Xnode communication
	http.HandleFunc("/", receiveXnodeData)
	log.Fatalf("xnode listener error: %v", http.ListenAndServe(*addr, nil))

	// Run forever.
	select {}
}

func NewApplication() *Application {
	return &Application{Validators: make(map[string]AbciValidator), VerifiedData: make(map[string]VerifiedDataItem)}
}

func (app *Application) Info(_ context.Context, info *types.RequestInfo) (*types.ResponseInfo, error) {
	verifiedData, err := json.Marshal(app.VerifiedData)
	if err != nil {
		return &types.ResponseInfo{
			Data: fmt.Sprintf("Something went wrong parsing verified data err %v", err),
		}, err
	}

	validators, err := json.Marshal(app.Validators)
	if err != nil {
		return &types.ResponseInfo{
			Data: fmt.Sprintf("Something went wrong parsing validators err %v", err),
		}, err
	}

	return &types.ResponseInfo{Data: fmt.Sprintf("{\"VerifiedData\":%v,\"Validators\":%v,\"TotalUpdates\":%v}", string(verifiedData), string(validators), app.TotalTransactions)}, nil
}

func (app *Application) Query(_ context.Context, req *types.RequestQuery) (*types.ResponseQuery, error) {
	switch req.Path {
	case "tx":
		return &types.ResponseQuery{Value: []byte(fmt.Sprintf("%v", app.TotalTransactions))}, nil
	default:
		return &types.ResponseQuery{Log: fmt.Sprintf("Invalid query path. Expected price, time or tx, got %v", req.Path)}, nil
	}
}

func (app *Application) CheckTx(_ context.Context, check *types.RequestCheckTx) (*types.ResponseCheckTx, error) {
	tx := &Transaction{}
	err := json.Unmarshal(check.Tx, tx)
	if err != nil {
		return &types.ResponseCheckTx{
			Code: CodeTypeTransactionTypeDecodingError,
			Log:  fmt.Sprint("Not able to parse transaction type", "err", err),
		}, err
	}

	switch tx.TransactionType {
	case TransactionValidateData:
		validateDataTx := &ValidateDataTx{}
		err := json.Unmarshal(check.Tx, validateDataTx)
		if err != nil {
			return &types.ResponseCheckTx{
				Code: CodeTypeTransactionDecodingError,
				Log:  fmt.Sprint("Not able to parse validate data transaction", "err", err),
			}, err
		}

		latestAllowedTimestamp := uint64(time.Now().Unix()) - 1 // Validators should have at least 1 second to receive the data
		if validateDataTx.DataTimestamp >= latestAllowedTimestamp {
			// Is this exploitable? Evil validators accepting transcations that are just under 1 second
			// low latency validators not accepting, higher latency validators do accept (low latency validators get punished?)

			return &types.ResponseCheckTx{
				Code: CodeTypeDataTooNew,
				Log: fmt.Sprintf("New transaction timestamp is not old enough (attempted: %d, latest accepted: %d)",
					validateDataTx.DataTimestamp,
					latestAllowedTimestamp,
				),
			}, errors.New("new transaction timestamp is not old enough")
		}

		if validateDataTx.DataTimestamp <= app.VerifiedData[validateDataTx.DataFeed].Timestamp {
			return &types.ResponseCheckTx{
				Code: CodeTypeDataOutdated,
				Log: fmt.Sprintf("New transaction timestamp is not newer than latest one (attempted: %d, latest: %d)",
					validateDataTx.DataTimestamp,
					app.VerifiedData[validateDataTx.DataFeed].Timestamp,
				),
			}, errors.New("new transaction timestamp is not newer than latest one")
		}
		dataFromXnode, exists := verifiedXnodeData[validateDataTx.DataFeed][validateDataTx.DataTimestamp]
		if !exists || dataFromXnode != validateDataTx.DataValue {
			return &types.ResponseCheckTx{
				Code: CodeTypeDataNotVerified,
				Log: fmt.Sprintf("New transaction data is not confirmed by our xnode (attempted: %v at %d)",
					validateDataTx.DataValue,
					validateDataTx.DataTimestamp,
				),
			}, errors.New("new transaction data is not confirmed by our xnode")
		}

	case TransactionStakeTokens:
		stakeTokensTx := &StakeTokensTx{}
		err := json.Unmarshal(check.Tx, stakeTokensTx)
		if err != nil {
			return &types.ResponseCheckTx{
				Code: CodeTypeTransactionDecodingError,
				Log:  fmt.Sprint("Not able to parse stake tokens transaction", "err", err),
			}, err
		}

		validator := app.Validators[stakeTokensTx.ValidatorAddress]
		if stakeTokensTx.Amount > 0 && validator.Tokens-stakeTokensTx.Amount >= 0 {
			return &types.ResponseCheckTx{
				Code: CodeTypeNotEnoughUnstakedTokens,
				Log:  fmt.Sprintf("Trying to stake more tokens than unstaked (attempted: %d, unstaked: %d)", stakeTokensTx.Amount, validator.Tokens),
			}, errors.New("trying to stake more tokens than unstaked")
		}
		if stakeTokensTx.Amount < 0 && validator.GovernancePower+stakeTokensTx.Amount >= 0 {
			return &types.ResponseCheckTx{
				Code: CodeTypeNotEnoughStakedTokens,
				Log:  fmt.Sprintf("Trying to unstake more tokens than staked (attemped: %d, staked: %d)", -stakeTokensTx.Amount, validator.GovernancePower),
			}, errors.New("trying to unstake more tokens than staked")
		}

		verifier := ed25519.NewBatchVerifier()
		hasher := sha256.New()
		// We can also calculate the byte arrays more efficiently (e.g. using binary.LittleEndian) and write them seperately
		// However this significantly increases code cluther with the convertion process and error handling
		_, err = hasher.Write([]byte("Stake" + fmt.Sprintf("%#x", stakeTokensTx.Amount) + fmt.Sprintf("%#x", (validator.Nonce))))
		if err != nil {
			return &types.ResponseCheckTx{
				Code: CodeTypeInvalidSingature,
				Log:  "Error hashing data for proof validation",
			}, err
		}
		err = verifier.Add(validator.PubKey, hasher.Sum(nil), []byte(stakeTokensTx.Proof))
		if err != nil {
			return &types.ResponseCheckTx{
				Code: CodeTypeInvalidSingature,
				Log:  "Error verifying proof",
			}, err
		}
		valid, _ := verifier.Verify()
		if !valid {
			return &types.ResponseCheckTx{
				Code: CodeTypeInvalidSingature,
				Log:  "Proof is not valid",
			}, err
		}

	case TransactionClaimTokens:
		claimTokensTx := &ClaimTokensTx{}
		err := json.Unmarshal(check.Tx, claimTokensTx)
		if err != nil {
			return &types.ResponseCheckTx{
				Code: CodeTypeTransactionDecodingError,
				Log:  fmt.Sprint("Not able to parse claim tokens transaction", "err", err),
			}, err
		}

		deposit, exists := verifiedDeposits[claimTokensTx.TransactionHash]
		if !exists {
			// Does this also need a timestamp to check if it's not too recent?
			return &types.ResponseCheckTx{
				Code: CodeTypeDepositNotVerified,
				Log:  fmt.Sprintf("Deposit is not confirmed by our xnode (attempted: %v)", claimTokensTx.TransactionHash),
			}, errors.New("deposit is not confirmed by our xnode")
		}

		data := []byte("hello")
		hash := eth.Keccak256Hash(data)
		signerAddress, err := eth.Ecrecover(hash.Bytes(), []byte(claimTokensTx.Proof))
		if err != nil || bytes.HexBytes(signerAddress).String() != deposit.Address {
			return &types.ResponseCheckTx{
				Code: CodeTypeDepositInvalidSignature,
				Log:  fmt.Sprintf("Signature does not match depositer address, it should be signed by: %v", deposit.Address),
			}, err
		}

	case TransactionWithdrawTokens:
		withdrawTokensTx := &WithdrawTokensTx{}
		err := json.Unmarshal(check.Tx, withdrawTokensTx)
		if err != nil {
			return &types.ResponseCheckTx{
				Code: CodeTypeTransactionDecodingError,
				Log:  fmt.Sprint("Not able to parse withdraw tokens transaction", "err", err),
			}, err
		}

		validator := app.Validators[withdrawTokensTx.ValidatorAddress]
		if withdrawTokensTx.Amount > validator.Tokens {
			return &types.ResponseCheckTx{
				Code: CodeTypeNotEnoughUnstakedTokens,
				Log:  fmt.Sprintf("Trying to stake more tokens than unstaked (attempted: %d, unstaked: %d)", withdrawTokensTx.Amount, validator.Tokens),
			}, errors.New("trying to stake more tokens than unstaked")
		}

		verifier := ed25519.NewBatchVerifier()
		hasher := sha256.New()
		_, err = hasher.Write([]byte("Withdraw" + fmt.Sprintf("%#x", withdrawTokensTx.Amount) + withdrawTokensTx.Address + fmt.Sprintf("%#x", (validator.Nonce))))
		if err != nil {
			return &types.ResponseCheckTx{
				Code: CodeTypeInvalidSingature,
				Log:  "Error hashing data for proof validation",
			}, err
		}
		err = verifier.Add(validator.PubKey, hasher.Sum(nil), []byte(withdrawTokensTx.Proof))
		if err != nil {
			return &types.ResponseCheckTx{
				Code: CodeTypeInvalidSingature,
				Log:  "Error verifying proof",
			}, err
		}
		valid, _ := verifier.Verify()
		if !valid {
			return &types.ResponseCheckTx{
				Code: CodeTypeInvalidSingature,
				Log:  "Proof is not valid",
			}, err
		}

	}

	return &types.ResponseCheckTx{Code: CodeTypeOK}, nil
}

func (app *Application) InitChain(_ context.Context, chain *types.RequestInitChain) (*types.ResponseInitChain, error) {
	for i := 0; i < len(chain.Validators); i++ {
		pk := ed25519.PubKey(chain.Validators[i].PubKey.GetEd25519())
		app.Validators[pk.Address().String()] = AbciValidator{
			PubKey:          pk,
			GovernancePower: chain.Validators[i].Power,
			Tokens:          0,
		}
	}
	return &types.ResponseInitChain{}, nil
}

func (app *Application) FinalizeBlock(context context.Context, req *types.RequestFinalizeBlock) (*types.ResponseFinalizeBlock, error) {
	// Process transactions
	txs := make([]*types.ExecTxResult, len(req.Txs))
	events := make([]types.Event, 0, len(req.Txs)) // Change if a proposal can have more than 1 event
	for i := 0; i < len(req.Txs); i++ {
		// Check again as state changes between mempool addition and process could have invalidated it
		check, _ := app.CheckTx(context, &types.RequestCheckTx{Tx: req.Txs[i]})
		if check.Code != CodeTypeOK {
			txs[i] = &types.ExecTxResult{
				Code: check.Code,
				Log:  check.Log,
			}
			continue
		}

		tx := &Transaction{}
		err := json.Unmarshal(req.Txs[i], tx)
		if err != nil {
			txs[i] = &types.ExecTxResult{
				Code: CodeTypeTransactionDecodingError,
				Log:  check.Log,
			}
			continue
		}

		switch tx.TransactionType {
		case TransactionValidateData:
			validateDataTx := &ValidateDataTx{}
			err := json.Unmarshal(req.Txs[i], validateDataTx)
			if err != nil {
				txs[i] = &types.ExecTxResult{
					Code: CodeTypeTransactionTypeDecodingError,
					Log:  check.Log,
				}
				continue
			}

			app.VerifiedData[validateDataTx.DataFeed] = VerifiedDataItem{Data: validateDataTx.DataValue, Timestamp: validateDataTx.DataTimestamp}

			event := types.Event{Type: "Data Verified", Attributes: make([]types.EventAttribute, 3)}
			event.Attributes[0] = types.EventAttribute{Key: "feed", Value: fmt.Sprintf("%v", validateDataTx.DataFeed)}
			event.Attributes[1] = types.EventAttribute{Key: "data", Value: fmt.Sprintf("%v", validateDataTx.DataValue)}
			event.Attributes[2] = types.EventAttribute{Key: "timestamp", Value: fmt.Sprintf("%d", validateDataTx.DataTimestamp)}
			events = append(events, event)

		case TransactionStakeTokens:
			stakeTokensTx := &StakeTokensTx{}
			err := json.Unmarshal(req.Txs[i], stakeTokensTx)
			if err != nil {
				txs[i] = &types.ExecTxResult{
					Code: CodeTypeTransactionTypeDecodingError,
					Log:  check.Log,
				}
				continue
			}

			validator := app.Validators[stakeTokensTx.ValidatorAddress]

			// Amount can be negative to unstake
			validator.GovernancePower += stakeTokensTx.Amount
			validator.Tokens -= stakeTokensTx.Amount

			// Only relevant if amount is negative
			if validator.GovernancePower < minimumValidatorPower {
				// If their GovernancePower is bellow the threshold, move all to tokens and give them GovernancePower 0
				validator.Tokens += validator.GovernancePower
				validator.GovernancePower = 0
			}

			validator.Nonce++

			app.Validators[stakeTokensTx.ValidatorAddress] = validator

			event := types.Event{Type: "Tokens Staked", Attributes: make([]types.EventAttribute, 2)}
			event.Attributes[0] = types.EventAttribute{Key: "validator", Value: fmt.Sprintf("%v", stakeTokensTx.ValidatorAddress)}
			event.Attributes[1] = types.EventAttribute{Key: "amount", Value: fmt.Sprintf("%d", stakeTokensTx.Amount)}
			events = append(events, event)
			// Do we want to include the proof in here too?

		case TransactionClaimTokens:
			claimTokensTx := &ClaimTokensTx{}
			err := json.Unmarshal(req.Txs[i], claimTokensTx)
			if err != nil {
				txs[i] = &types.ExecTxResult{
					Code: CodeTypeTransactionTypeDecodingError,
					Log:  check.Log,
				}
				continue
			}

			validator := app.Validators[claimTokensTx.ValidatorAddress]

			deposit := verifiedDeposits[claimTokensTx.TransactionHash]
			validator.Tokens += deposit.Amount
			delete(verifiedDeposits, claimTokensTx.TransactionHash) // Prevent deposit from being claimed again

			app.Validators[claimTokensTx.ValidatorAddress] = validator

			event := types.Event{Type: "Token Claimed", Attributes: make([]types.EventAttribute, 2)}
			event.Attributes[0] = types.EventAttribute{Key: "validator", Value: fmt.Sprintf("%v", claimTokensTx.ValidatorAddress)}
			event.Attributes[1] = types.EventAttribute{Key: "transactionhash", Value: fmt.Sprintf("%v", claimTokensTx.TransactionHash)}
			events = append(events, event)
			// Do we want to include the proof in here too?
			// Do we want to inlcude deposit info (you can check that on Ethereum with transaction hash tho)

		case TransactionWithdrawTokens:
			withdrawTokensTx := &WithdrawTokensTx{}
			err := json.Unmarshal(req.Txs[i], withdrawTokensTx)
			if err != nil {
				txs[i] = &types.ExecTxResult{
					Code: check.Code,
					Log:  check.Log,
				}
				continue
			}

			validator := app.Validators[withdrawTokensTx.ValidatorAddress]

			validator.Tokens -= withdrawTokensTx.Amount

			validator.Nonce++

			app.Validators[withdrawTokensTx.ValidatorAddress] = validator

			event := types.Event{Type: "Tokens Withdrawn", Attributes: make([]types.EventAttribute, 2)}
			event.Attributes[0] = types.EventAttribute{Key: "validator", Value: fmt.Sprintf("%v", withdrawTokensTx.ValidatorAddress)}
			event.Attributes[1] = types.EventAttribute{Key: "amount", Value: fmt.Sprintf("%d", withdrawTokensTx.Amount)}
			events = append(events, event)
			// Do we want to include the proof in here too?

		}

		app.TotalTransactions++
		txs[i] = &types.ExecTxResult{Code: CodeTypeOK}
	}

	// Calculate block rewards (lagging behind 1 block, cannot know already who votes on this block obviously)
	// This assumes all punished validators are still validating though!
	blockRewards := make([]types.ValidatorUpdate, len(req.DecidedLastCommit.Votes)+len(req.Misbehavior))
	for i := 0; i < len(req.DecidedLastCommit.Votes); i++ {
		address := bytes.HexBytes(req.DecidedLastCommit.Votes[i].Validator.Address).String()
		validator := app.Validators[address]

		validator.GovernancePower += validator.GovernancePower / 10000
		blockRewards[i] = types.Ed25519ValidatorUpdate(validator.PubKey.Bytes(), validator.GovernancePower)

		app.Validators[address] = validator
	}
	for i := 0; i < len(req.Misbehavior); i++ {
		address := bytes.HexBytes(req.Misbehavior[i].Validator.Address).String()
		validator := app.Validators[address]

		// Can a validator withdraw their governance power before the evidence against their actions is finalized to prevent punishment?
		validator.GovernancePower -= validator.GovernancePower / 100
		if validator.GovernancePower < minimumValidatorPower {
			// If their GovernancePower is bellow the threshold, move all to tokens and give them GovernancePower 0
			validator.Tokens += validator.GovernancePower
			validator.GovernancePower = 0
		}
		blockRewards[len(req.DecidedLastCommit.Votes)+i] = types.Ed25519ValidatorUpdate(validator.PubKey.Bytes(), validator.GovernancePower)

		app.Validators[address] = validator
	}
	return &types.ResponseFinalizeBlock{TxResults: txs, ValidatorUpdates: blockRewards, Events: events}, nil
}
