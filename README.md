# Dependencies

https://go.dev/doc/install (to build tendermint)

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

### Tendermints docs

https://docs.tendermint.com/v0.34/tendermint-core/using-tendermint.html#transactions
