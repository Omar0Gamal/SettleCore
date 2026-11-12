// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import { Test } from "forge-std/Test.sol";
import { IERC20 } from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import { MessageHashUtils } from "@openzeppelin/contracts/utils/cryptography/MessageHashUtils.sol";
import { EscrowVault } from "../src/EscrowVault.sol";
import { IEscrowVault } from "../src/interfaces/IEscrowVault.sol";
import { MockUSDC } from "./mocks/MockUSDC.sol";
import { MockWBTC } from "./mocks/MockWBTC.sol";
import { MockUSDT } from "./mocks/MockUSDT.sol";

contract EscrowVaultTest is Test {
    EscrowVault internal vault;
    MockUSDC internal usdc;
    MockWBTC internal wbtc;
    MockUSDT internal usdt;

    uint256 internal hotKey = 0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d;
    uint256 internal coldKey = 0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a;
    address internal hotSigner;
    address internal coldSigner;
    address internal user = makeAddr("user");
    address internal attacker = makeAddr("attacker");

    bytes32 internal constant EIP712_DOMAIN_TYPEHASH = keccak256(
        "EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"
    );

    function setUp() public {
        hotSigner = vm.addr(hotKey);
        coldSigner = vm.addr(coldKey);

        vault = new EscrowVault(address(this), hotSigner, coldSigner);
        usdc = new MockUSDC();
        wbtc = new MockWBTC();
        usdt = new MockUSDT();

        vault.setTokenConfig(address(0), true, 10 ether, 25 ether);
        vault.setTokenConfig(address(usdc), true, 10_000e6, 25_000e6);
        vault.setTokenConfig(address(wbtc), true, 1e8, 5e8);
        vault.setTokenConfig(address(usdt), true, 10_000e6, 25_000e6);

        vm.deal(user, 100 ether);
        usdc.mint(user, 1_000_000e6);
        wbtc.mint(user, 50e8);
        usdt.mint(user, 1_000_000e6);
    }

    function _domainSeparator() internal view returns (bytes32) {
        return keccak256(
            abi.encode(
                EIP712_DOMAIN_TYPEHASH,
                keccak256("SettleCoreEscrowVault"),
                keccak256("1"),
                block.chainid,
                address(vault)
            )
        );
    }

    function _signVoucher(uint256 privKey, IEscrowVault.WithdrawalVoucher memory v)
        internal
        view
        returns (bytes memory)
    {
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
        bytes32 digest = MessageHashUtils.toTypedDataHash(_domainSeparator(), structHash);
        (uint8 vByte, bytes32 r, bytes32 s) = vm.sign(privKey, digest);
        return abi.encodePacked(r, s, vByte);
    }

    function test_DepositETH_And_Receive() public {
        vm.startPrank(user);
        vm.expectEmit(true, true, false, true);
        emit IEscrowVault.Deposited(user, address(0), 2 ether, block.timestamp);
        vault.depositETH{ value: 2 ether }();

        (bool sent,) = address(vault).call{ value: 3 ether }("");
        assertTrue(sent);
        vm.stopPrank();

        assertEq(address(vault).balance, 5 ether);
    }

    function test_DepositERC20_USDC_And_WBTC() public {
        vm.startPrank(user);
        usdc.approve(address(vault), 50_000e6);
        vault.depositERC20(address(usdc), 50_000e6);

        wbtc.approve(address(vault), 5e8);
        vault.depositERC20(address(wbtc), 5e8);
        vm.stopPrank();

        assertEq(usdc.balanceOf(address(vault)), 50_000e6);
        assertEq(wbtc.balanceOf(address(vault)), 5e8);
    }

    function test_DepositAndWithdraw_NonStandardUSDT() public {
        // Confirm MockUSDT.transfer returns empty data (0 bytes), which breaks naive IERC20 callers
        vm.prank(user);
        usdt.transfer(address(this), 100e6);
        (bool rawOk, bytes memory retData) =
            address(usdt).call(abi.encodeCall(IERC20.transfer, (user, 100e6)));
        assertTrue(rawOk);
        assertEq(retData.length, 0);

        vm.startPrank(user);
        usdt.approve(address(vault), 20_000e6);
        vault.depositERC20(address(usdt), 20_000e6);
        vm.stopPrank();
        assertEq(usdt.balanceOf(address(vault)), 20_000e6);

        IEscrowVault.WithdrawalVoucher memory v = IEscrowVault.WithdrawalVoucher({
            withdrawalId: keccak256("w-usdt-1"),
            recipient: user,
            token: address(usdt),
            amount: 5000e6,
            nonce: 1,
            deadline: block.timestamp + 1 hours
        });
        bytes memory hotSig = _signVoucher(hotKey, v);

        uint256 beforeBal = usdt.balanceOf(user);
        vault.withdraw(v, hotSig, "");
        assertEq(usdt.balanceOf(user), beforeBal + 5000e6);
        assertEq(usdt.balanceOf(address(vault)), 15_000e6);
    }

    function test_Withdraw_HotOnly_UnderCap() public {
        vm.prank(user);
        vault.depositETH{ value: 20 ether }();

        IEscrowVault.WithdrawalVoucher memory v = IEscrowVault.WithdrawalVoucher({
            withdrawalId: keccak256("w-eth-1"),
            recipient: user,
            token: address(0),
            amount: 5 ether,
            nonce: 10,
            deadline: block.timestamp + 1 hours
        });
        bytes memory hotSig = _signVoucher(hotKey, v);

        uint256 beforeBal = user.balance;
        vm.expectEmit(true, true, true, true);
        emit IEscrowVault.WithdrawalExecuted(v.withdrawalId, user, address(0), 5 ether, 10, false);
        vault.withdraw(v, hotSig, "");

        assertEq(user.balance, beforeBal + 5 ether);
        assertEq(vault.rollingHotUsed(address(0)), 5 ether);
        assertTrue(vault.usedNonces(10));
    }

    function test_Withdraw_Revert_AbovePerTxCapWithoutColdSig() public {
        vm.prank(user);
        usdc.approve(address(vault), 50_000e6);
        vm.prank(user);
        vault.depositERC20(address(usdc), 50_000e6);

        IEscrowVault.WithdrawalVoucher memory v = IEscrowVault.WithdrawalVoucher({
            withdrawalId: keccak256("w-cap-1"),
            recipient: user,
            token: address(usdc),
            amount: 10_001e6,
            nonce: 20,
            deadline: block.timestamp + 1 hours
        });
        bytes memory hotSig = _signVoucher(hotKey, v);

        vm.expectRevert(IEscrowVault.InvalidColdSignature.selector);
        vault.withdraw(v, hotSig, "");
    }

    function test_Withdraw_HotAndColdCosigned_AbovePerTxCap() public {
        vm.prank(user);
        usdc.approve(address(vault), 50_000e6);
        vm.prank(user);
        vault.depositERC20(address(usdc), 50_000e6);

        IEscrowVault.WithdrawalVoucher memory v = IEscrowVault.WithdrawalVoucher({
            withdrawalId: keccak256("w-cold-1"),
            recipient: user,
            token: address(usdc),
            amount: 30_000e6,
            nonce: 30,
            deadline: block.timestamp + 1 hours
        });
        bytes memory hotSig = _signVoucher(hotKey, v);
        bytes memory coldSig = _signVoucher(coldKey, v);

        uint256 beforeBal = usdc.balanceOf(user);
        vm.expectEmit(true, true, true, true);
        emit IEscrowVault.WithdrawalExecuted(
            v.withdrawalId, user, address(usdc), 30_000e6, 30, true
        );
        vault.withdraw(v, hotSig, coldSig);

        assertEq(usdc.balanceOf(user), beforeBal + 30_000e6);
        assertEq(vault.rollingHotUsed(address(usdc)), 0);
    }

    function test_Withdraw_Rolling24hDailyCap_RequiresColdSig_AndResetsNextDay() public {
        vm.prank(user);
        usdc.approve(address(vault), 100_000e6);
        vm.prank(user);
        vault.depositERC20(address(usdc), 100_000e6);

        // First two hot withdrawals use 20,000 USDC of 25,000 USDC daily cap
        for (uint256 i = 1; i <= 2; ++i) {
            IEscrowVault.WithdrawalVoucher memory v = IEscrowVault.WithdrawalVoucher({
                withdrawalId: bytes32(i),
                recipient: user,
                token: address(usdc),
                amount: 10_000e6,
                nonce: i,
                deadline: block.timestamp + 2 days
            });
            vault.withdraw(v, _signVoucher(hotKey, v), "");
        }
        assertEq(vault.rollingHotUsed(address(usdc)), 20_000e6);

        // Next 6,000 USDC exceeds 25,000 USDC daily cap -> hot-only reverts
        IEscrowVault.WithdrawalVoucher memory v3 = IEscrowVault.WithdrawalVoucher({
            withdrawalId: bytes32(uint256(3)),
            recipient: user,
            token: address(usdc),
            amount: 6000e6,
            nonce: 3,
            deadline: block.timestamp + 2 days
        });
        bytes memory hotSig3 = _signVoucher(hotKey, v3);
        vm.expectRevert(IEscrowVault.InvalidColdSignature.selector);
        vault.withdraw(v3, hotSig3, "");

        // With cold co-signature it succeeds
        vault.withdraw(v3, hotSig3, _signVoucher(coldKey, v3));

        // After warping 1 day, rolling 24h token bucket decays to 0 and hot-only succeeds again
        vm.warp(block.timestamp + 1 days);
        IEscrowVault.WithdrawalVoucher memory v4 = IEscrowVault.WithdrawalVoucher({
            withdrawalId: bytes32(uint256(4)),
            recipient: user,
            token: address(usdc),
            amount: 6000e6,
            nonce: 4,
            deadline: block.timestamp + 1 hours
        });
        vault.withdraw(v4, _signVoucher(hotKey, v4), "");
        assertEq(vault.rollingHotUsed(address(usdc)), 6000e6);
    }

    function test_Rolling24hCapPreventsMidnightBoundaryDoubleDrain() public {
        // Configure per-tx cap at 20,000 USDC and daily cap at 25,000 USDC
        vault.setTokenConfig(address(usdc), true, 20_000e6, 25_000e6);
        vm.startPrank(user);
        usdc.approve(address(vault), 100_000e6);
        vault.depositERC20(address(usdc), 100_000e6);
        vm.stopPrank();

        // Warp to 23:59:00 UTC on an arbitrary day (20_000 * 1 days - 60)
        uint256 midnight = 20_000 * 1 days;
        vm.warp(midnight - 60);

        // Withdraw 70% of daily cap (17,500 USDC) with hot signature only
        IEscrowVault.WithdrawalVoucher memory v1 = IEscrowVault.WithdrawalVoucher({
            withdrawalId: keccak256("midnight-1"),
            recipient: user,
            token: address(usdc),
            amount: 17_500e6,
            nonce: 201,
            deadline: block.timestamp + 1 hours
        });
        vault.withdraw(v1, _signVoucher(hotKey, v1), "");
        assertEq(vault.rollingHotUsed(address(usdc)), 17_500e6);

        // Warp 2 minutes across UTC midnight to 00:01:00 UTC
        vm.warp(midnight + 60);
        uint256 expectedUsed = 17_500e6 - (17_500e6 * uint256(120)) / 1 days;
        assertEq(vault.currentRollingHotUsed(address(usdc)), expectedUsed);

        // Another 50% (12,500 USDC) hot-only withdrawal MUST revert with InvalidColdSignature
        IEscrowVault.WithdrawalVoucher memory v2 = IEscrowVault.WithdrawalVoucher({
            withdrawalId: keccak256("midnight-2"),
            recipient: user,
            token: address(usdc),
            amount: 12_500e6,
            nonce: 202,
            deadline: block.timestamp + 1 hours
        });
        bytes memory hotSig2 = _signVoucher(hotKey, v2);
        vm.expectRevert(IEscrowVault.InvalidColdSignature.selector);
        vault.withdraw(v2, hotSig2, "");

        // Cold co-signature still allows legitimate withdrawal without altering hot bucket
        vault.withdraw(v2, hotSig2, _signVoucher(coldKey, v2));
        assertEq(vault.rollingHotUsed(address(usdc)), 17_500e6);
    }

    function test_Withdraw_Revert_ReplayNonce() public {
        vm.prank(user);
        vault.depositETH{ value: 10 ether }();

        IEscrowVault.WithdrawalVoucher memory v = IEscrowVault.WithdrawalVoucher({
            withdrawalId: keccak256("w-replay"),
            recipient: user,
            token: address(0),
            amount: 1 ether,
            nonce: 77,
            deadline: block.timestamp + 1 hours
        });
        bytes memory hotSig = _signVoucher(hotKey, v);
        vault.withdraw(v, hotSig, "");

        vm.expectRevert(abi.encodeWithSelector(IEscrowVault.NonceAlreadyUsed.selector, 77));
        vault.withdraw(v, hotSig, "");
    }

    function test_Withdraw_Revert_ExpiredDeadline() public {
        vm.prank(user);
        vault.depositETH{ value: 5 ether }();

        IEscrowVault.WithdrawalVoucher memory v = IEscrowVault.WithdrawalVoucher({
            withdrawalId: keccak256("w-exp"),
            recipient: user,
            token: address(0),
            amount: 1 ether,
            nonce: 88,
            deadline: block.timestamp + 10
        });
        bytes memory hotSig = _signVoucher(hotKey, v);

        vm.warp(block.timestamp + 11);
        vm.expectRevert(IEscrowVault.VoucherExpired.selector);
        vault.withdraw(v, hotSig, "");
    }

    function test_Withdraw_Revert_WrongSigner() public {
        vm.prank(user);
        vault.depositETH{ value: 20 ether }();

        uint256 wrongKey = 0x123456;
        IEscrowVault.WithdrawalVoucher memory v = IEscrowVault.WithdrawalVoucher({
            withdrawalId: keccak256("w-bad-sig"),
            recipient: user,
            token: address(0),
            amount: 1 ether,
            nonce: 99,
            deadline: block.timestamp + 1 hours
        });

        bytes memory badHotSig = _signVoucher(wrongKey, v);
        vm.expectRevert(IEscrowVault.InvalidHotSignature.selector);
        vault.withdraw(v, badHotSig, "");

        IEscrowVault.WithdrawalVoucher memory vCold = IEscrowVault.WithdrawalVoucher({
            withdrawalId: keccak256("w-bad-cold"),
            recipient: user,
            token: address(0),
            amount: 15 ether,
            nonce: 100,
            deadline: block.timestamp + 1 hours
        });
        bytes memory goodHotSig = _signVoucher(hotKey, vCold);
        bytes memory badColdSig = _signVoucher(wrongKey, vCold);
        vm.expectRevert(IEscrowVault.InvalidColdSignature.selector);
        vault.withdraw(vCold, goodHotSig, badColdSig);
    }

    function test_WithdrawAndDeposit_Revert_ValidationAndPause() public {
        address unlisted = makeAddr("unlisted");

        vm.expectRevert(IEscrowVault.InvalidToken.selector);
        vault.depositERC20(unlisted, 100);

        vm.expectRevert(IEscrowVault.ZeroAmount.selector);
        vault.depositETH{ value: 0 }();

        vm.expectRevert(IEscrowVault.ZeroAmount.selector);
        vault.depositERC20(address(usdc), 0);

        vault.setPaused(true);
        vm.expectRevert(IEscrowVault.EnforcedPause.selector);
        vault.depositETH{ value: 1 ether }();

        IEscrowVault.WithdrawalVoucher memory v = IEscrowVault.WithdrawalVoucher({
            withdrawalId: bytes32(uint256(1)),
            recipient: user,
            token: address(0),
            amount: 1 ether,
            nonce: 1,
            deadline: block.timestamp + 1 hours
        });
        vm.expectRevert(IEscrowVault.EnforcedPause.selector);
        vault.withdraw(v, "", "");

        vault.setPaused(false);
        v.recipient = address(0);
        vm.expectRevert(IEscrowVault.ZeroAddress.selector);
        vault.withdraw(v, "", "");

        v.recipient = user;
        v.token = unlisted;
        vm.expectRevert(IEscrowVault.InvalidToken.selector);
        vault.withdraw(v, "", "");

        v.token = address(0);
        v.amount = 0;
        vm.expectRevert(IEscrowVault.ZeroAmount.selector);
        vault.withdraw(v, "", "");
    }

    function test_PublishReserveRoot() public {
        bytes32 root1 = keccak256("reserve-root-1");
        vm.expectEmit(true, true, false, true);
        emit IEscrowVault.ReserveRootPublished(root1, 100, block.timestamp);
        vault.publishReserveRoot(root1, 100);
        assertEq(vault.latestReserveRoot(), root1);
        assertEq(vault.latestReserveBlockHeight(), 100);

        address attesterAddr = makeAddr("attester");
        vm.expectEmit(true, false, false, false);
        emit IEscrowVault.AttesterUpdated(attesterAddr);
        vault.setAttester(attesterAddr);
        assertEq(vault.attester(), attesterAddr);

        bytes32 root2 = keccak256("reserve-root-2");
        vm.prank(attesterAddr);
        vault.publishReserveRoot(root2, 101);
        assertEq(vault.latestReserveRoot(), root2);
        assertEq(vault.latestReserveBlockHeight(), 101);

        vm.prank(attacker);
        vm.expectRevert(IEscrowVault.Unauthorized.selector);
        vault.publishReserveRoot(keccak256("bad"), 102);
    }

    function test_CompromisedHotSignerCannotPublishReserveRoot() public {
        vm.prank(hotSigner);
        vm.expectRevert(IEscrowVault.Unauthorized.selector);
        vault.publishReserveRoot(keccak256("forged-root"), 999);

        vm.prank(hotSigner);
        vm.expectRevert(IEscrowVault.Unauthorized.selector);
        vault.publishReserveAttestation(keccak256("forged-root"), 999, address(0), 1 ether);
    }

    function test_OnChainIsSolventAttestation() public {
        vm.startPrank(user);
        vault.depositETH{ value: 10 ether }();
        usdc.approve(address(vault), 50_000e6);
        vault.depositERC20(address(usdc), 50_000e6);
        vm.stopPrank();

        address attesterAddr = makeAddr("dedicatedAttester");
        vm.expectRevert(IEscrowVault.ZeroAddress.selector);
        vault.setAttester(address(0));

        vm.prank(attacker);
        vm.expectRevert();
        vault.setAttester(attacker);

        vault.setAttester(attesterAddr);

        bytes32 rootEth = keccak256("sum-root-eth");
        vm.prank(attesterAddr);
        vm.expectEmit(true, true, true, true);
        emit IEscrowVault.ReserveAttestationPublished(
            rootEth, 100, address(0), 10 ether, block.timestamp
        );
        vault.publishReserveAttestation(rootEth, 100, address(0), 10 ether);
        assertEq(vault.publishedLiabilities(address(0)), 10 ether);
        assertTrue(vault.isSolvent(address(0)));

        vm.prank(attesterAddr);
        vault.publishReserveAttestation(rootEth, 101, address(0), 10 ether + 1);
        assertFalse(vault.isSolvent(address(0)));

        bytes32 rootUsdc = keccak256("sum-root-usdc");
        vm.prank(attesterAddr);
        vault.publishReserveAttestation(rootUsdc, 102, address(usdc), 50_000e6);
        assertTrue(vault.isSolvent(address(usdc)));

        vm.prank(attesterAddr);
        vault.publishReserveAttestation(rootUsdc, 103, address(usdc), 50_001e6);
        assertFalse(vault.isSolvent(address(usdc)));
    }

    function test_AdminControls() public {
        address newHot = makeAddr("newHot");
        address newCold = makeAddr("newCold");

        vm.expectRevert(IEscrowVault.ZeroAddress.selector);
        vault.setSigners(address(0), newCold);

        vm.expectEmit(true, true, false, false);
        emit IEscrowVault.SignersUpdated(newHot, newCold);
        vault.setSigners(newHot, newCold);
        assertEq(vault.hotSigner(), newHot);
        assertEq(vault.coldSigner(), newCold);

        vm.prank(attacker);
        vm.expectRevert();
        vault.setTokenConfig(address(usdc), false, 0, 0);
    }
}
