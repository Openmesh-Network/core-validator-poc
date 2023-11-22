// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

import {ERC20Votes, Votes, ERC20} from "@openzeppelin/contracts/token/ERC20/extensions/ERC20Votes.sol";
import {EIP712} from "@openzeppelin/contracts/utils/cryptography/EIP712.sol";
import {AccessControl} from "@openzeppelin/contracts/access/AccessControl.sol";

import {IERC20MintBurnable} from "./IERC20MintBurnable.sol";

contract OPEN is ERC20Votes, AccessControl, IERC20MintBurnable {
    bytes32 public constant MINT_ROLE = keccak256("MINT");
    uint256 immutable maxSupply;

    error SurpassMaxSupply();

    constructor(
        string memory _name,
        string memory _symbol,
        uint256 _maxSupply,
        address _admin
    ) ERC20(_name, _symbol) EIP712(_name, "1") {
        maxSupply = _maxSupply;
        _grantRole(DEFAULT_ADMIN_ROLE, _admin);
    }

    /// @inheritdoc IERC20MintBurnable
    function mint(
        address account,
        uint256 amount
    ) external onlyRole(MINT_ROLE) {
        if (totalSupply() + amount > maxSupply) {
            revert SurpassMaxSupply();
        }

        _mint(account, amount);
    }

    /// @inheritdoc IERC20MintBurnable
    function burn(uint256 amount) external {
        _burn(msg.sender, amount);
    }
}
