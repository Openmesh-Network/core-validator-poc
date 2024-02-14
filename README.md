# XNode Validator Proof of Concept 

This project implements a proof of stake blockchain that validates mock exchange transactions.

## Dependencies
- Make
- Docker (Make sure you've got docker permissions on your machine)
- Go compiler
- Npm

## Libraries
- CommetBFT
  - See general docs [here](https://docs.cometbft.com/v0.38/)
  - See RPC docs [here](https://docs.cometbft.com/v0.38/rpc/#/) you can use them to get more info on the system.

## Project layout
- `xnode-app` has the go program that runs the validator node logic
- `data-mock` has a simple JS server that mocks live transactions
- `smart-contracts` stores initial implementation of smart contracts that bridge ETH and OPEN chains 

## How to run
1. Install dependencies `make install` (only needs to be done once)
2. Build with `make build`
3. Run the actual program `make run`

## Usage
Send a transaction:
```
curl http://localhost:26657/broadcast_tx_commit?tx=0x00
```

Get state of system:
```
curl http://localhost:26657/abci_info
```

For more actions check out the CommetBFT rpc docs [here](https://docs.cometbft.com/v0.38/rpc/#/).

## Get up to speed

If you want to look at the code, we recommend opening the xnode-app folder in your IDE.
Opening up this folder instead might break the Go linting.

### CommetBFT / PoS Blockchain

### Bridging

### 
