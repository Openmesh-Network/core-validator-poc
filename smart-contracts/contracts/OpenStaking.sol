// SPDX-License-Identifier: UNLICENSED
pragma solidity ^0.8.0;

import {Ownable} from "@openzeppelin/contracts/access/Ownable.sol";

import {IERC20MintBurnable} from "./IERC20MintBurnable.sol";
import {IOpenStaking} from "./IOpenStaking.sol";

contract OpenStaking is Ownable, IOpenStaking {
    IERC20MintBurnable private immutable token;
    mapping(address => uint256) private withdrawable;

    constructor(IERC20MintBurnable _token, address _admin) Ownable(_admin) {
        token = _token;
    }

    /// @inheritdoc IOpenStaking
    function stake(uint256 _amount) external {
        token.transferFrom(msg.sender, address(this), _amount);
        token.burn(_amount);
        emit TokensStaked(msg.sender, _amount);
    }

    /// @inheritdoc IOpenStaking
    function withdraw(uint256 _amount) external {
        if (_amount > withdrawable[msg.sender]) {
            revert NotEnoughWithdrawableTokens();
        }

        token.mint(msg.sender, _amount);
        emit TokensWithdrawn(msg.sender, _amount);
    }

    // CHANGE TO PROOF BASED (user pays gas fee) ?
    // (Address (prevent frontrun), amount, nonce (prevent replay)) ?
    // Proof would be built into withdraw function directly, no need for the mapping anymore
    function addWithdrawable(
        address[] calldata _accounts,
        uint256[] calldata _amounts
    ) external onlyOwner {
        if (_accounts.length != _amounts.length) {
            revert ArraysAreNotEqualLength();
        }

        for (uint i; i < _accounts.length; ) {
            withdrawable[_accounts[i]] += _amounts[i];
            emit TokensWithdrawable(_accounts[i], _amounts[i]);

            unchecked {
                ++i;
            }
        }
    }
}
