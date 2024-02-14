install:
	make --directory=./cometbft build
.PHONY: install

build:
	docker build --tag tendermint-app ./xnode-app
	docker build --tag xnode ./data-mock
	rm -rf ./cometbft/build/node*
	make --directory=./cometbft build-docker-localnode
	docker run --rm -v $(CURDIR)/cometbft/build:/cometbft:Z cometbft/localnode testnet --config /etc/cometbft/config-template.toml --o . --starting-ip-address 192.166.10.2
	find ./cometbft/build/node*/config/genesis.json | xargs sed -i 's/"power": "1"/"power": "10000000000000"/g'
	find ./cometbft/build/node*/config/config.toml | xargs sed -i 's/create_empty_blocks = true/create_empty_blocks = false/g'
	find ./cometbft/build/node*/config/config.toml | xargs sed -i 's/send_rate = 5120000/send_rate = 5120000000/g'
	find ./cometbft/build/node*/config/config.toml | xargs sed -i 's/recv_rate = 5120000/recv_rate = 5120000000/g'
.PHONY: build

run:
	docker compose -f ./docker-compose.yml up
.PHONY: run

