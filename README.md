# Dependencies

https://go.dev/doc/install (to build tendermint)  
https://docs.docker.com/engine/install/ (to run the abci app and the tendermint nodes)  

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

### Tendermint rpc docs

https://docs.tendermint.com/v0.34/rpc/#/
