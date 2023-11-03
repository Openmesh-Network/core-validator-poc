// SPDX-License-Identifier: UNLICENSED
pragma solidity ^0.8.0;

interface IOpenStaking {
    error NotEnoughWithdrawableTokens();
    error ArraysAreNotEqualLength();

    event TokensStaked(address indexed account, uint256 amount);
    event TokensWithdrawn(address indexed account, uint256 amount);
    event TokensWithdrawable(address indexed account, uint256 amount);

    /// Stake tokens to become a validator on the xnode network.
    /// @param _amount The amount of tokens to stake.
    function stake(uint256 _amount) external;

    /// Claim your withdrawn tokens from the xnode network.
    /// @param _amount The amount of tokens to claim.
    function withdraw(uint256 _amount) external;
}
