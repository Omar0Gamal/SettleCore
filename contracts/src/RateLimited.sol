// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/// @title RateLimited
/// @notice Continuous linear-decay 24h rolling token-bucket rate limiter per asset.
abstract contract RateLimited {
    mapping(address => bool) public isAllowedToken;
    mapping(address => uint256) public hotPerTxCap;
    mapping(address => uint256) public hotDailyCap;
    mapping(address => uint256) public rollingHotUsed;
    mapping(address => uint256) public lastHotTimestamp;

    /// @notice Returns the current decayed rolling 24h hot outflow for `token`.
    function currentRollingHotUsed(address token) external view returns (uint256) {
        return _currentHotUsed(token);
    }

    function _currentHotUsed(address token) internal view returns (uint256) {
        uint256 elapsed = block.timestamp - lastHotTimestamp[token];
        uint256 prevUsed = rollingHotUsed[token];
        uint256 decay = elapsed >= 1 days ? prevUsed : (prevUsed * elapsed) / 1 days;
        return prevUsed > decay ? prevUsed - decay : 0;
    }

    function _consumeHotQuota(address token, uint256 amount) internal returns (bool requiresCold) {
        uint256 currentUsed = _currentHotUsed(token);
        uint256 cap = hotDailyCap[token];
        requiresCold =
            amount > hotPerTxCap[token] || currentUsed >= cap || amount > cap - currentUsed;
        if (!requiresCold) {
            rollingHotUsed[token] = currentUsed + amount;
            lastHotTimestamp[token] = block.timestamp;
        }
    }
}
