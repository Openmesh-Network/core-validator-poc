install:
	make --directory=./tendermint build-linux
.PHONY: install

update:
	docker build --tag tendermint-app ./xnode-app
	rm -rf ./tendermint/build/node*
	make --directory=./tendermint build-docker-localnode
.PHONY: update

start:
	make --directory=./tendermint localnet-start
.PHONY: start

