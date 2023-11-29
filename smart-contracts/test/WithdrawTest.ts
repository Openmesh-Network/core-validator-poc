import { deployments, ethers, getNamedAccounts, getUnnamedAccounts } from "hardhat";
import { expect } from "chai";
import { OPEN, OpenWithdrawing } from "../typechain-types";
import { Ether } from "../utils/ethersUnits";
import { Signature } from "ethers";
import { erc20admin } from "../settings";

describe("Withdraw Test", () => {
  it("should allow to withdraw with correct signature", async () => {
    await deployments.fixture(["OpenWithdrawing"]);
    const { deployer } = await getNamedAccounts();
    const OpenWithdrawing = (await ethers.getContract("OpenWithdrawing", deployer)) as OpenWithdrawing;
    const owner = await ethers.getSigner(deployer);

    const oldOwnerAddress = await OpenWithdrawing.owner();
    const oldOwner = await ethers.getImpersonatedSigner(oldOwnerAddress);
    await owner.sendTransaction({ to: oldOwnerAddress, value: Ether(1) });
    await OpenWithdrawing.connect(oldOwner).transferOwnership(deployer);

    const OPEN = (await ethers.getContract("OPEN", deployer)) as OPEN;
    const OPENAdmin = await ethers.getImpersonatedSigner(erc20admin);
    await owner.sendTransaction({ to: erc20admin, value: Ether(1) });
    await OPEN.connect(OPENAdmin).grantRole(ethers.keccak256(ethers.toUtf8Bytes("MINT")), await OpenWithdrawing.getAddress());

    const withdrawAddress = (await getUnnamedAccounts())[0];
    const withdrawer = await ethers.getSigner(withdrawAddress);
    const amount = 1;

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
    const message = { withdrawer: withdrawAddress, nonce: 0, amount: amount };

    const signatureString = await owner.signTypedData(domain, types, message);
    const signature = Signature.from(signatureString);

    const tx = OpenWithdrawing.connect(withdrawer).withdraw(signature.v, signature.r, signature.s, withdrawAddress, amount);
    await expect(tx).to.not.be.reverted;
  });
});
