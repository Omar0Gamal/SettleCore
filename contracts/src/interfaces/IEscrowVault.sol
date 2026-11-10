// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

interface IEscrowVault {
    struct WithdrawalVoucher {
        bytes32 withdrawalId;
        address recipient;
        address token;
        uint256 amount;
        uint256 nonce;
        uint256 deadline;
    }

    event Deposited(
        address indexed depositor, address indexed token, uint256 amount, uint256 timestamp
    );
    event WithdrawalExecuted(
        bytes32 indexed withdrawalId,
        address indexed recipient,
        address indexed token,
        uint256 amount,
        uint256 nonce,
        bool coldCosigned
    );
    event ReserveRootPublished(
        bytes32 indexed merkleRoot, uint64 indexed blockHeight, uint256 timestamp
    );
    event ReserveAttestationPublished(
        bytes32 indexed merkleRoot,
        uint64 indexed blockHeight,
        address indexed token,
        uint256 totalLiability,
        uint256 timestamp
    );
    event TokenConfigUpdated(
        address indexed token, bool allowed, uint256 hotPerTxCap, uint256 hotDailyCap
    );
    event SignersUpdated(address indexed hotSigner, address indexed coldSigner);
    event AttesterUpdated(address indexed attester);

    error InvalidToken();
    error ZeroAmount();
    error ZeroAddress();
    error VoucherExpired();
    error NonceAlreadyUsed(uint256 nonce);
    error InvalidHotSignature();
    error InvalidColdSignature();
    error ETHTransferFailed();
    error Unauthorized();
    error EnforcedPause();

    function depositETH() external payable;
    function depositERC20(address token, uint256 amount) external;
    function withdraw(
        WithdrawalVoucher calldata voucher,
        bytes calldata hotSig,
        bytes calldata coldSig
    ) external;
    function publishReserveRoot(bytes32 merkleRoot, uint64 blockHeight) external;
    function publishReserveAttestation(
        bytes32 merkleRoot,
        uint64 blockHeight,
        address token,
        uint256 totalLiability
    ) external;
    function setAttester(address newAttester) external;
    function isSolvent(address token) external view returns (bool);
}
