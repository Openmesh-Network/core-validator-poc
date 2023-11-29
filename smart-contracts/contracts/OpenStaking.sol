// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

import {IERC20MintBurnable} from "./IERC20MintBurnable.sol";
import {IOpenStaking} from "./IOpenStaking.sol";

contract OpenStaking is IOpenStaking {
    IERC20MintBurnable private immutable token;

    constructor(IERC20MintBurnable _token) {
        token = _token;
    }

    /// @inheritdoc IOpenStaking
    function stake(uint256 _amount) external {
        token.transferFrom(msg.sender, address(this), _amount);
        token.burn(_amount);
        emit TokensStaked(msg.sender, _amount);
    }
}
