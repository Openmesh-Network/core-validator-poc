# In xnode-app repo:

## To update:

sudo docker build --tag tendermint-app .

# In tendermint repo:

## Once:

make build-linux

## To update:

sudo rm -rf ./build/node\*
sudo make build-docker-localnode

## To start:

sudo make localnet-start
