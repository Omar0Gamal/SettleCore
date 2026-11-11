// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import { Ownable } from "@openzeppelin/contracts/access/Ownable.sol";
import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { IEscrowVault } from "./interfaces/IEscrowVault.sol";

/// @title ReserveAttestation
/// @notice Role-separated Proof-of-Reserves attestation and on-chain solvency checks.
abstract contract ReserveAttestation is IEscrowVault, Ownable {
    address public attester;
    bytes32 public latestReserveRoot;
    uint64 public latestReserveBlockHeight;
    mapping(address => uint256) public publishedLiabilities;

    function publishReserveRoot(bytes32 merkleRoot, uint64 blockHeight) external {
        _publishRoot(merkleRoot, blockHeight);
    }

    function publishReserveAttestation(
        bytes32 merkleRoot,
        uint64 blockHeight,
        address token,
        uint256 totalLiability
    ) external {
        _publishRoot(merkleRoot, blockHeight);
        publishedLiabilities[token] = totalLiability;
        emit ReserveAttestationPublished(
            merkleRoot, blockHeight, token, totalLiability, block.timestamp
        );
    }

    function setAttester(address newAttester) external onlyOwner {
        if (newAttester == address(0)) revert ZeroAddress();
        attester = newAttester;
        emit AttesterUpdated(newAttester);
    }

    function isSolvent(address token) external view returns (bool) {
        uint256 bal =
            token == address(0) ? address(this).balance : IERC20(token).balanceOf(address(this));
        return bal >= publishedLiabilities[token];
    }

    function _publishRoot(bytes32 merkleRoot, uint64 blockHeight) internal {
        if (msg.sender != owner() && msg.sender != attester) revert Unauthorized();
        latestReserveRoot = merkleRoot;
        latestReserveBlockHeight = blockHeight;
        emit ReserveRootPublished(merkleRoot, blockHeight, block.timestamp);
    }
}
