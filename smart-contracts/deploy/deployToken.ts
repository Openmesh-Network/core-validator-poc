import { HardhatRuntimeEnvironment } from "hardhat/types";
import { DeployFunction } from "hardhat-deploy/types";
import { erc20admin, erc20Name, erc20Ticker, maxSupply } from "../settings";

const func: DeployFunction = async function (hre: HardhatRuntimeEnvironment) {
  const { deployments, getNamedAccounts } = hre;

  await deployments.save("OPEN", { address: "0xD8076a366012d402D7699dd24c3Ae744cc6E6E90", ...(await deployments.getExtendedArtifact("OPEN")) });
  return;

  const { deployer } = await getNamedAccounts();

  await deployments.deploy("OPEN", {
    from: deployer,
    args: [erc20Name, erc20Ticker, maxSupply, erc20admin],
  });
};
export default func;
func.tags = ["OPEN"];
