import { ethers } from "hardhat";
import { Signature } from "ethers";
import { Ether } from "../utils/ethersUnits";

const withdrawer = "0xaF7E68bCb2Fc7295492A00177f14F59B92814e70";
const amount = Ether(100);
const OpenWithdrawingAddress = "0x734eBF68D9634086157c8E655f177Ad9C99DAD7B";

async function main() {
  const [owner] = await ethers.getSigners();
  const OpenWithdrawing = await ethers.getContractAt("OpenWithdrawing", OpenWithdrawingAddress);

  const domainInfo = await OpenWithdrawing.eip712Domain();
  const domain = {
    name: domainInfo.name,
    version: domainInfo.version,
    chainId: Number(domainInfo.chainId),
    verifyingContract: domainInfo.verifyingContract,
  };
  const types = {
    Withdraw: [
      { name: "withdrawer", type: "address" },
      { name: "nonce", type: "uint256" },
      { name: "amount", type: "uint256" },
    ],
  };
  const message = { withdrawer: withdrawer, nonce: 0, amount: amount };

  const signatureString = await owner.signTypedData(domain, types, message);
  const signature = Signature.from(signatureString);

  console.log({ v: signature.v, r: signature.r, s: signature.s, withdrawer: withdrawer, amount: amount });
}

// We recommend this pattern to be able to use async/await everywhere
// and properly handle errors.
main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
