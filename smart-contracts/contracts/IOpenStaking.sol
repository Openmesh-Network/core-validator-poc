// SPDX-License-Identifier: UNLICENSED
pragma solidity ^0.8.0;

interface IOpenStaking {
    error InvalidProof();

    event TokensStaked(address indexed account, uint256 amount);
    event TokensWithdrawn(address indexed account, uint256 amount);
    event TokensWithdrawable(address indexed account, uint256 amount);

    /// Stake tokens to become a validator on the xnode network.
    /// @param _amount The amount of tokens to stake.
    function stake(uint256 _amount) external;

    /// Claim your withdrawn tokens from the xnode network.
    /// @param _v V component of proof signature.
    /// @param _r R component of proof signature.
    /// @param _s S component of proof signature.
    /// @param _withdrawer To which address to send the tokens.
    /// @param _amount How many tokens to claim.
    function withdraw(
        uint8 _v,
        bytes32 _r,
        bytes32 _s,
        address _withdrawer,
        uint256 _amount
    ) external;
}
