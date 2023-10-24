install:
	make --directory=./tendermint build
.PHONY: install

update:
	docker build --tag tendermint-app ./xnode-app
	docker build --tag xnode ./data-mock
	rm -rf ./tendermint/build/node*
	make --directory=./tendermint build-docker-localnode
.PHONY: update

start:
	make --directory=./tendermint localnet-start
.PHONY: start

