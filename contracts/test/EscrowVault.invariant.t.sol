// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import { Test } from "forge-std/Test.sol";
import { StdInvariant } from "forge-std/StdInvariant.sol";
import { MessageHashUtils } from "@openzeppelin/contracts/utils/cryptography/MessageHashUtils.sol";
import { EscrowVault } from "../src/EscrowVault.sol";
import { IEscrowVault } from "../src/interfaces/IEscrowVault.sol";
import { MockUSDC } from "./mocks/MockUSDC.sol";
import { MockUSDT } from "./mocks/MockUSDT.sol";

contract EscrowVaultHandler is Test {
    EscrowVault public immutable vault;
    MockUSDC public immutable usdc;
    MockUSDT public immutable usdt;

    uint256 internal immutable hotKey;
    uint256 internal immutable coldKey;
    address internal immutable actor = makeAddr("invariantActor");

    uint256 public ghostEthDeposited;
    uint256 public ghostEthWithdrawn;
    uint256 public ghostUsdcDeposited;
    uint256 public ghostUsdcWithdrawn;
    uint256 public ghostUsdtDeposited;
    uint256 public ghostUsdtWithdrawn;
    uint256 public nextNonce = 1;

    mapping(address => uint256) public ghostRollingHotUsed;
    mapping(address => uint256) public ghostLastHotTimestamp;

    bytes32 internal constant EIP712_DOMAIN_TYPEHASH = keccak256(
        "EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"
    );

    constructor(
        EscrowVault _vault,
        MockUSDC _usdc,
        MockUSDT _usdt,
        uint256 _hotKey,
        uint256 _coldKey
    ) {
        vault = _vault;
        usdc = _usdc;
        usdt = _usdt;
        hotKey = _hotKey;
        coldKey = _coldKey;
    }

    function depositETH(uint96 rawAmount) external {
        uint256 amount = bound(uint256(rawAmount), 1, 50 ether);
        vm.deal(actor, amount);
        vm.prank(actor);
        vault.depositETH{ value: amount }();
        ghostEthDeposited += amount;
    }

    function depositERC20(uint8 tokenIdx, uint96 rawAmount) external {
        uint256 amount = bound(uint256(rawAmount), 1, 100_000e6);
        if (tokenIdx % 2 == 0) {
            usdc.mint(actor, amount);
            vm.startPrank(actor);
            usdc.approve(address(vault), amount);
            vault.depositERC20(address(usdc), amount);
            vm.stopPrank();
            ghostUsdcDeposited += amount;
        } else {
            usdt.mint(actor, amount);
            vm.startPrank(actor);
            usdt.approve(address(vault), amount);
            vault.depositERC20(address(usdt), amount);
            vm.stopPrank();
            ghostUsdtDeposited += amount;
        }
    }

    function withdrawHot(uint8 assetIdx, uint96 rawAmount) external {
        address token = _tokenByIndex(assetIdx);
        uint256 bal = _vaultBalance(token);
        if (bal == 0) return;

        uint256 currentUsed = _ghostCurrentUsed(token);
        uint256 dailyCap = vault.hotDailyCap(token);
        if (currentUsed >= dailyCap) return;

        uint256 maxHot = dailyCap - currentUsed;
        uint256 perTxCap = vault.hotPerTxCap(token);
        if (perTxCap < maxHot) maxHot = perTxCap;
        if (bal < maxHot) maxHot = bal;
        if (maxHot == 0) return;

        uint256 amount = bound(uint256(rawAmount), 1, maxHot);
        uint256 nonce = nextNonce++;

        IEscrowVault.WithdrawalVoucher memory v = IEscrowVault.WithdrawalVoucher({
            withdrawalId: keccak256(abi.encode("hot", nonce)),
            recipient: actor,
            token: token,
            amount: amount,
            nonce: nonce,
            deadline: block.timestamp + 1 hours
        });

        vault.withdraw(v, _signVoucher(hotKey, v), "");
        _recordWithdrawn(token, amount);
        ghostRollingHotUsed[token] = currentUsed + amount;
        ghostLastHotTimestamp[token] = block.timestamp;
    }

    function withdrawCold(uint8 assetIdx, uint96 rawAmount) external {
        address token = _tokenByIndex(assetIdx);
        uint256 bal = _vaultBalance(token);
        if (bal == 0) return;

        uint256 amount = bound(uint256(rawAmount), 1, bal);
        uint256 nonce = nextNonce++;

        uint256 currentUsed = _ghostCurrentUsed(token);
        uint256 dailyCap = vault.hotDailyCap(token);
        bool requiresCold = amount > vault.hotPerTxCap(token) || currentUsed >= dailyCap
            || amount > dailyCap - currentUsed;

        IEscrowVault.WithdrawalVoucher memory v = IEscrowVault.WithdrawalVoucher({
            withdrawalId: keccak256(abi.encode("cold", nonce)),
            recipient: actor,
            token: token,
            amount: amount,
            nonce: nonce,
            deadline: block.timestamp + 1 hours
        });

        bytes memory hotSig = _signVoucher(hotKey, v);
        bytes memory coldSig = requiresCold ? _signVoucher(coldKey, v) : bytes("");
        vault.withdraw(v, hotSig, coldSig);

        _recordWithdrawn(token, amount);
        if (!requiresCold) {
            ghostRollingHotUsed[token] = currentUsed + amount;
            ghostLastHotTimestamp[token] = block.timestamp;
        }
    }

    function tryHotPublishRoot(bytes32 root, uint64 height) external {
        vm.prank(vm.addr(hotKey));
        try vault.publishReserveRoot(root, height) {
            revert("hotSigner must never publish reserve root");
        } catch { }
    }

    function warpTime(uint32 deltaSeconds) external {
        uint256 delta = bound(uint256(deltaSeconds), 1, 2 days);
        vm.warp(block.timestamp + delta);
    }

    function _ghostCurrentUsed(address token) internal view returns (uint256) {
        uint256 elapsed = block.timestamp - ghostLastHotTimestamp[token];
        uint256 prevUsed = ghostRollingHotUsed[token];
        uint256 decay = elapsed >= 1 days ? prevUsed : (prevUsed * elapsed) / 1 days;
        return prevUsed > decay ? prevUsed - decay : 0;
    }

    function _tokenByIndex(uint8 idx) internal view returns (address) {
        uint256 mod = idx % 3;
        if (mod == 0) return address(0);
        if (mod == 1) return address(usdc);
        return address(usdt);
    }

    function _vaultBalance(address token) internal view returns (uint256) {
        if (token == address(0)) return address(vault).balance;
        if (token == address(usdc)) return usdc.balanceOf(address(vault));
        return usdt.balanceOf(address(vault));
    }

    function _recordWithdrawn(address token, uint256 amount) internal {
        if (token == address(0)) ghostEthWithdrawn += amount;
        else if (token == address(usdc)) ghostUsdcWithdrawn += amount;
        else ghostUsdtWithdrawn += amount;
    }

    function _signVoucher(uint256 privKey, IEscrowVault.WithdrawalVoucher memory v)
        internal
        view
        returns (bytes memory)
    {
        bytes32 domainSep = keccak256(
            abi.encode(
                EIP712_DOMAIN_TYPEHASH,
                keccak256("SettleCoreEscrowVault"),
                keccak256("1"),
                block.chainid,
                address(vault)
            )
        );
        bytes32 structHash = keccak256(
            abi.encode(
                vault.WITHDRAWAL_TYPEHASH(),
                v.withdrawalId,
                v.recipient,
                v.token,
                v.amount,
                v.nonce,
                v.deadline
            )
        );
        bytes32 digest = MessageHashUtils.toTypedDataHash(domainSep, structHash);
        (uint8 vByte, bytes32 r, bytes32 s) = vm.sign(privKey, digest);
        return abi.encodePacked(r, s, vByte);
    }
}

contract EscrowVaultInvariantTest is StdInvariant, Test {
    EscrowVault internal vault;
    MockUSDC internal usdc;
    MockUSDT internal usdt;
    EscrowVaultHandler internal handler;

    uint256 internal hotKey = 0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d;
    uint256 internal coldKey = 0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a;

    function setUp() public {
        vault = new EscrowVault(address(this), vm.addr(hotKey), vm.addr(coldKey));
        usdc = new MockUSDC();
        usdt = new MockUSDT();

        vault.setTokenConfig(address(0), true, 10 ether, 25 ether);
        vault.setTokenConfig(address(usdc), true, 10_000e6, 25_000e6);
        vault.setTokenConfig(address(usdt), true, 10_000e6, 25_000e6);

        handler = new EscrowVaultHandler(vault, usdc, usdt, hotKey, coldKey);
        targetContract(address(handler));
    }

    function invariant_vaultBalanceMatchesAccounting() public view {
        assertEq(address(vault).balance, handler.ghostEthDeposited() - handler.ghostEthWithdrawn());
        assertEq(
            usdc.balanceOf(address(vault)),
            handler.ghostUsdcDeposited() - handler.ghostUsdcWithdrawn()
        );
        assertEq(
            usdt.balanceOf(address(vault)),
            handler.ghostUsdtDeposited() - handler.ghostUsdtWithdrawn()
        );
    }

    function invariant_hotOutflowNeverExceedsCapWithoutColdSig() public view {
        address[3] memory tokens = [address(0), address(usdc), address(usdt)];
        for (uint256 i = 0; i < tokens.length; ++i) {
            address token = tokens[i];
            uint256 dailyCap = vault.hotDailyCap(token);
            assertLe(vault.rollingHotUsed(token), dailyCap);
            assertLe(vault.currentRollingHotUsed(token), dailyCap);
            assertEq(vault.rollingHotUsed(token), handler.ghostRollingHotUsed(token));
            assertEq(vault.lastHotTimestamp(token), handler.ghostLastHotTimestamp(token));
        }
    }

    function invariant_hotSignerCannotMutateReserveRoot() public view {
        assertEq(vault.latestReserveRoot(), bytes32(0));
        assertTrue(vault.attester() != vm.addr(hotKey));
    }
}
