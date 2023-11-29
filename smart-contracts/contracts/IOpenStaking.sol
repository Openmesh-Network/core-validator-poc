// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

interface IOpenStaking {
    event TokensStaked(address indexed account, uint256 amount);

    /// Stake tokens to become a validator on the xnode network.
    /// @param _amount The amount of tokens to stake.
    function stake(uint256 _amount) external;
}
