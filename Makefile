install:
	make --directory=./tendermint build
.PHONY: install

update:
	docker build --tag tendermint-app ./xnode-app
	docker build --tag xnode ./data-mock
	rm -rf ./tendermint/build/node*
	make --directory=./tendermint build-docker-localnode
	docker run --rm -v $(CURDIR)/tendermint/build:/tendermint:Z tendermint/localnode testnet --config /etc/tendermint/config-template.toml --o . --starting-ip-address 192.167.10.2
	find ./tendermint/build/node*/config/genesis.json | xargs sed -i 's/"power": "1"/"power": "10000000000000"/g'
.PHONY: update

start:
	docker compose -f ./tendermint/docker-compose.yml up
.PHONY: start

