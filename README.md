# Dependencies

https://go.dev/doc/install (to build tendermint)  
https://docs.docker.com/engine/install/ (to run the abci app and the tendermint nodes)

# Development

It is recommended to open the xnode-app folder in your preferred editor for GO projects. Opening this root folder instead might give errors in your IDE that its an invalid GO project.

# Commands

## Once:

```
make install
```

## To update:

```
sudo make update
```

## To start:

```
sudo make start
```

## While running

### Send a transaction

```
curl http://localhost:26657/broadcast_tx_commit?tx=0x00
```

### Get current state

```
curl http://localhost:26657/abci_info
```

### CometBFT rpc docs

https://docs.cometbft.com/v0.38/rpc/#/

### CometBFT general docs

https://docs.cometbft.com/v0.38/
