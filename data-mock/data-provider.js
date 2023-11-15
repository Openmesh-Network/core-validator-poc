const WebSocket = require("ws");
const axios = require("axios");
const { createPublicClient, http, parseAbi } = require("viem");
const { polygonMumbai } = require("viem/chains");

const nodeAddress = process.argv[2];
const lastPrice = {};
const lastTimestamp = {};

let abci;

setTimeout(start, 2000); // Wait for the tendermint node and abci to start up and connect to eachother
function start() {
  const binanceBTCUSDT = new WebSocket("wss://data-stream.binance.vision/ws/btcusdt@aggTrade");
  const binanceETHUSDT = new WebSocket("wss://data-stream.binance.vision/ws/ethusdt@aggTrade");
  abci = new WebSocket("ws://" + nodeAddress + ":8088");

  abci.on("open", () => {
    console.log("ABCI connected!");
  });
  abci.on("message", (data) => {
    console.log("received: %s", data);
  });

  binanceBTCUSDT.on("open", () => {
    console.log("Binance BTCUSDT connected!");
  });
  binanceBTCUSDT.on("message", handleMessage);

  binanceETHUSDT.on("open", () => {
    console.log("Binance ETHUSDT connected!");
  });
  binanceETHUSDT.on("message", handleMessage);

  const client = createPublicClient({
    chain: polygonMumbai,
    transport: http(),
  });
  const openstaking = {
    address: "0xB88E40E8289665EE3e7493753d79D5C0606F384C", // OpenStaking contract address
    abi: parseAbi(["event TokensStaked(address indexed account, uint256 amount)"]),
  };
  client.watchContractEvent({
    ...openstaking,
    eventName: "TokensStaked",
    onLogs: (logs) => {
      const {
        transactionHash,
        args: { account, amount },
      } = logs[0];

      console.log(account, "staked", amount);

      handleStake(transactionHash, account, amount);
    },
    onError: (error) => {
      console.error("OpenStaking watch error", error);
    },
  });

  // Early birds
  const validatorpassstaking = {
    address: "0xeeFbA1882Bc5d0775002b0618C051a0B052EE6dE", // ValidatorPass ERC721 contract address
    abi: parseAbi(["event Transfer(address indexed from, address indexed to, uint256 indexed tokenId)"]),
  };
  client.watchContractEvent({
    ...validatorpassstaking,
    eventName: "Transfer",
    args: { from: "0x0000000000000000000000000000000000000000" },
    onLogs: (logs) => {
      const {
        transactionHash,
        args: { to },
      } = logs[0];
      const account = to;
      const amount = BigInt(10_000) * (BigInt(10) ^ BigInt(18));

      console.log(account, "early staked", amount);

      handleStake(transactionHash, account, amount);
    },
    onError: (error) => {
      console.error("ValidatorPass watch error", error);
    },
  });
}

function handleStake(transactionHash, account, amount) {
  const json = JSON.stringify({
    MessageType: 1, // Add verified deposit
    TransactionHash: transactionHash,
    DepositInfo: {
      Address: account,
      // Send over amount in 2 integers? Doing this here is a bit unintuative (but doing in the app reduces precision)
      Amount: Number(amount / (BigInt(10) ^ BigInt(9))), // Risky!
    },
  });
  const message = [...Buffer.from(json)];
  abci.send(message, (err) => {
    if (err) {
      console.error("deposit communcication error", err);
      return;
    }
  });
}

function handleMessage(data) {
  const info = JSON.parse(data);
  const price = info.p;
  const timestamp = Math.round(info.E / 1000); // 1s timestamps (first event of the second decides the price)
  if (price != lastPrice[info.s]) {
    if (timestamp == lastTimestamp[info.s]) {
      return; // max 1 update per timestamp, not applicable to streams updating on a set interval
    }

    console.log(`${info.s} is ${info.p} at ${info.E}`);
    const json = JSON.stringify({
      MessageType: 0, // Add verified Xnode data
      DataFeed: "Binance|" + info.s + "|price", // Source|Item|property ? Decide a nice format lol
      DataValue: price,
      DataTimestamp: timestamp,
    });
    const message = [...Buffer.from(json)];
    abci.send(message, (err) => {
      if (err) {
        console.error("xnode communcication error", err);
        return;
      }

      // Some algorithm to decide which node should make transactions should be put in place here
      // Two nodes generating the same transaction is no problem, just a possible waste of bandwith
      if (nodeAddress == "192.166.10.2") {
        setTimeout(async () => {
          const json = JSON.stringify({
            TransactionType: 0, // Try to reach consensus on data
            DataFeed: "Binance|" + info.s + "|price", // Source|Item|property ? Decide a nice format lol
            DataValue: price,
            DataTimestamp: timestamp,
          });
          const tx = [...Buffer.from(json)];
          const url = "http://" + nodeAddress + ":26657/broadcast_tx_sync?tx=0x" + toHexString(tx);
          try {
            console.log("trying transaction", url, `(${price} at ${timestamp})`);
            const data = await axios.request(url);
            console.log("transcation response:", data.data);
          } catch (err) {
            console.error(err?.response?.data ?? err);
          }
        }, 2500); // Ensure that the transaction time is (> data time + 1 sec) in UTC seconds (maybe we can reduce it, I am not sure how it's rounded)
      }
    });

    lastPrice[info.s] = price;
    lastTimestamp[info.s] = timestamp;
  }
}

function toHexString(bytes) {
  return Array.from(bytes, (byte) => {
    return ("0" + (byte & 0xff).toString(16)).slice(-2);
  }).join("");
}
