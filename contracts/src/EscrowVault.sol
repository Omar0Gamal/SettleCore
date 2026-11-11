// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import { Ownable } from "@openzeppelin/contracts/access/Ownable.sol";
import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { SafeERC20 } from "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import { ReentrancyGuard } from "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import { ECDSA } from "@openzeppelin/contracts/utils/cryptography/ECDSA.sol";
import { EIP712 } from "@openzeppelin/contracts/utils/cryptography/EIP712.sol";
import { RateLimited } from "./RateLimited.sol";
import { ReserveAttestation } from "./ReserveAttestation.sol";

/// @title EscrowVault
/// @notice Tiered Hot/Cold EIP-712 custody vault with rolling 24h token-bucket rate caps.
contract EscrowVault is EIP712, ReentrancyGuard, RateLimited, ReserveAttestation {
    using SafeERC20 for IERC20;

    bytes32 public constant WITHDRAWAL_TYPEHASH = keccak256(
        "Withdrawal(bytes32 withdrawalId,address recipient,address token,uint256 amount,uint256 nonce,uint256 deadline)"
    );

    address public hotSigner;
    address public coldSigner;
    bool public paused;
    mapping(uint256 => bool) public usedNonces;

    constructor(address initialOwner, address _hotSigner, address _coldSigner)
        EIP712("SettleCoreEscrowVault", "1")
        Ownable(initialOwner)
    {
        if (_hotSigner == address(0) || _coldSigner == address(0)) revert ZeroAddress();
        hotSigner = _hotSigner;
        coldSigner = _coldSigner;
        attester = initialOwner;
        emit SignersUpdated(_hotSigner, _coldSigner);
        emit AttesterUpdated(initialOwner);
    }

    receive() external payable nonReentrant {
        _depositETH();
    }

    function depositETH() external payable nonReentrant {
        _depositETH();
    }

    function depositERC20(address token, uint256 amount) external nonReentrant {
        if (paused) revert EnforcedPause();
        if (token == address(0) || !isAllowedToken[token]) revert InvalidToken();
        if (amount == 0) revert ZeroAmount();
        IERC20(token).safeTransferFrom(msg.sender, address(this), amount);
        emit Deposited(msg.sender, token, amount, block.timestamp);
    }

    function withdraw(WithdrawalVoucher calldata v, bytes calldata hotSig, bytes calldata coldSig)
        external
        nonReentrant
    {
        if (paused) revert EnforcedPause();
        if (v.recipient == address(0)) revert ZeroAddress();
        if (!isAllowedToken[v.token]) revert InvalidToken();
        if (v.amount == 0) revert ZeroAmount();
        if (block.timestamp > v.deadline) revert VoucherExpired();
        if (usedNonces[v.nonce]) revert NonceAlreadyUsed(v.nonce);
        bool cold = _authorizeWithdraw(v, hotSig, coldSig);
        usedNonces[v.nonce] = true;
        emit WithdrawalExecuted(v.withdrawalId, v.recipient, v.token, v.amount, v.nonce, cold);
        if (v.token == address(0)) {
            (bool ok,) = v.recipient.call{ value: v.amount }("");
            if (!ok) revert ETHTransferFailed();
        } else {
            IERC20(v.token).safeTransfer(v.recipient, v.amount);
        }
    }

    function setTokenConfig(address t, bool ok, uint256 perTx, uint256 daily) external onlyOwner {
        isAllowedToken[t] = ok;
        hotPerTxCap[t] = perTx;
        hotDailyCap[t] = daily;
        emit TokenConfigUpdated(t, ok, perTx, daily);
    }

    function setSigners(address _hotSigner, address _coldSigner) external onlyOwner {
        if (_hotSigner == address(0) || _coldSigner == address(0)) revert ZeroAddress();
        hotSigner = _hotSigner;
        coldSigner = _coldSigner;
        emit SignersUpdated(_hotSigner, _coldSigner);
    }

    function setPaused(bool _paused) external onlyOwner {
        paused = _paused;
    }

    function _depositETH() internal {
        if (paused) revert EnforcedPause();
        if (!isAllowedToken[address(0)]) revert InvalidToken();
        if (msg.value == 0) revert ZeroAmount();
        emit Deposited(msg.sender, address(0), msg.value, block.timestamp);
    }

    function _authorizeWithdraw(
        WithdrawalVoucher calldata v,
        bytes calldata hot,
        bytes calldata coldSig
    ) internal returns (bool cold) {
        bytes32 digest = _hashTypedDataV4(
            keccak256(
                abi.encode(
                    WITHDRAWAL_TYPEHASH,
                    v.withdrawalId,
                    v.recipient,
                    v.token,
                    v.amount,
                    v.nonce,
                    v.deadline
                )
            )
        );
        if (!_validSig(digest, hot, hotSigner)) revert InvalidHotSignature();
        cold = _consumeHotQuota(v.token, v.amount);
        if (cold && !_validSig(digest, coldSig, coldSigner)) revert InvalidColdSignature();
    }

    function _validSig(bytes32 d, bytes calldata sig, address signer) internal pure returns (bool) {
        (address recovered, ECDSA.RecoverError err,) = ECDSA.tryRecover(d, sig);
        return err == ECDSA.RecoverError.NoError && recovered == signer;
    }
}
