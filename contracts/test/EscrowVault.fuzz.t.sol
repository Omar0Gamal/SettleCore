// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import { Test } from "forge-std/Test.sol";
import { MessageHashUtils } from "@openzeppelin/contracts/utils/cryptography/MessageHashUtils.sol";
import { EscrowVault } from "../src/EscrowVault.sol";
import { IEscrowVault } from "../src/interfaces/IEscrowVault.sol";
import { MockUSDC } from "./mocks/MockUSDC.sol";

contract EscrowVaultFuzzTest is Test {
    EscrowVault internal vault;
    MockUSDC internal usdc;

    uint256 internal hotKey = 0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d;
    uint256 internal coldKey = 0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a;
    address internal user = makeAddr("fuzzUser");

    uint256 internal constant PER_TX_CAP = 10_000e6;
    uint256 internal constant DAILY_CAP = 25_000e6;

    bytes32 internal constant EIP712_DOMAIN_TYPEHASH = keccak256(
        "EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"
    );

    function setUp() public {
        vault = new EscrowVault(address(this), vm.addr(hotKey), vm.addr(coldKey));
        usdc = new MockUSDC();
        vault.setTokenConfig(address(usdc), true, PER_TX_CAP, DAILY_CAP);
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

    function testFuzz_DepositAndWithdrawERC20(uint96 depositAmt, uint96 withdrawAmt, uint256 nonce)
        public
    {
        uint256 dep = bound(uint256(depositAmt), 1, 1_000_000_000e6);
        uint256 wAmt = bound(uint256(withdrawAmt), 1, dep);

        usdc.mint(user, dep);
        vm.startPrank(user);
        usdc.approve(address(vault), dep);
        vault.depositERC20(address(usdc), dep);
        vm.stopPrank();

        IEscrowVault.WithdrawalVoucher memory v = IEscrowVault.WithdrawalVoucher({
            withdrawalId: keccak256(abi.encode(nonce, wAmt)),
            recipient: user,
            token: address(usdc),
            amount: wAmt,
            nonce: nonce,
            deadline: block.timestamp + 1 hours
        });
        bytes memory hotSig = _signVoucher(hotKey, v);
        bytes memory coldSig =
            (wAmt > PER_TX_CAP || wAmt > DAILY_CAP) ? _signVoucher(coldKey, v) : bytes("");

        vault.withdraw(v, hotSig, coldSig);

        assertEq(usdc.balanceOf(user), wAmt);
        assertEq(usdc.balanceOf(address(vault)), dep - wAmt);
        assertTrue(vault.usedNonces(nonce));
    }

    function testFuzz_RateLimitThresholdEnforcement(
        uint96 firstAmt,
        uint96 secondAmt,
        uint32 warpSeconds
    ) public {
        uint256 amt1 = bound(uint256(firstAmt), 1, PER_TX_CAP);
        uint256 amt2 = bound(uint256(secondAmt), 1, PER_TX_CAP);
        uint256 totalFunding = amt1 + amt2;

        usdc.mint(user, totalFunding);
        vm.startPrank(user);
        usdc.approve(address(vault), totalFunding);
        vault.depositERC20(address(usdc), totalFunding);
        vm.stopPrank();

        IEscrowVault.WithdrawalVoucher memory v1 = IEscrowVault.WithdrawalVoucher({
            withdrawalId: bytes32(uint256(1)),
            recipient: user,
            token: address(usdc),
            amount: amt1,
            nonce: 1,
            deadline: block.timestamp + 365 days
        });
        vault.withdraw(v1, _signVoucher(hotKey, v1), "");

        uint256 warp = uint256(warpSeconds);
        vm.warp(block.timestamp + warp);

        IEscrowVault.WithdrawalVoucher memory v2 = IEscrowVault.WithdrawalVoucher({
            withdrawalId: bytes32(uint256(2)),
            recipient: user,
            token: address(usdc),
            amount: amt2,
            nonce: 2,
            deadline: block.timestamp + 1 hours
        });
        bytes memory hotSig2 = _signVoucher(hotKey, v2);

        uint256 decay = warp >= 1 days ? amt1 : (amt1 * warp) / 1 days;
        uint256 currentUsed = amt1 - decay;
        bool exceedsDaily = currentUsed + amt2 > DAILY_CAP;

        if (exceedsDaily) {
            vm.expectRevert(IEscrowVault.InvalidColdSignature.selector);
            vault.withdraw(v2, hotSig2, "");
            vault.withdraw(v2, hotSig2, _signVoucher(coldKey, v2));
        } else {
            vault.withdraw(v2, hotSig2, "");
            assertLe(vault.rollingHotUsed(address(usdc)), DAILY_CAP);
        }
    }

    function testFuzz_ReplayProtection(uint256 nonce, uint96 amount) public {
        uint256 amt = bound(uint256(amount), 1, PER_TX_CAP);
        usdc.mint(user, amt * 2);

        vm.startPrank(user);
        usdc.approve(address(vault), amt * 2);
        vault.depositERC20(address(usdc), amt * 2);
        vm.stopPrank();

        IEscrowVault.WithdrawalVoucher memory v = IEscrowVault.WithdrawalVoucher({
            withdrawalId: keccak256(abi.encode("first", nonce)),
            recipient: user,
            token: address(usdc),
            amount: amt,
            nonce: nonce,
            deadline: block.timestamp + 1 hours
        });
        vault.withdraw(v, _signVoucher(hotKey, v), "");

        IEscrowVault.WithdrawalVoucher memory replay = IEscrowVault.WithdrawalVoucher({
            withdrawalId: keccak256(abi.encode("second", nonce)),
            recipient: user,
            token: address(usdc),
            amount: amt,
            nonce: nonce,
            deadline: block.timestamp + 2 hours
        });
        bytes memory hotSigReplay = _signVoucher(hotKey, replay);

        vm.expectRevert(abi.encodeWithSelector(IEscrowVault.NonceAlreadyUsed.selector, nonce));
        vault.withdraw(replay, hotSigReplay, "");
    }
}
