// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import { Script, console2 } from "forge-std/Script.sol";
import { EscrowVault } from "../src/EscrowVault.sol";
import { MockUSDC } from "../test/mocks/MockUSDC.sol";
import { MockWBTC } from "../test/mocks/MockWBTC.sol";
import { MockUSDT } from "../test/mocks/MockUSDT.sol";

contract DeployLocal is Script {
    uint256 internal constant DEFAULT_DEPLOYER_KEY =
        0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80;
    address internal constant DEFAULT_HOT_SIGNER = 0x70997970C51812dc3A010C7d01b50e0d17dc79C8;
    address internal constant DEFAULT_COLD_SIGNER = 0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC;

    function run()
        external
        returns (EscrowVault vault, MockUSDC usdc, MockWBTC wbtc, MockUSDT usdt)
    {
        uint256 deployerKey = vm.envOr("DEPLOYER_PRIVATE_KEY", DEFAULT_DEPLOYER_KEY);
        address deployer = vm.addr(deployerKey);
        address hotSigner = vm.envOr("HOT_SIGNER_ADDRESS", DEFAULT_HOT_SIGNER);
        address coldSigner = vm.envOr("COLD_SIGNER_ADDRESS", DEFAULT_COLD_SIGNER);

        vm.startBroadcast(deployerKey);

        // Deploy in deterministic nonce order matching .env.example addresses
        vault = new EscrowVault(deployer, hotSigner, coldSigner);
        usdc = new MockUSDC();
        wbtc = new MockWBTC();
        usdt = new MockUSDT();

        vault.setTokenConfig(address(0), true, 10 ether, 100 ether);
        vault.setTokenConfig(address(usdc), true, 25_000e6, 250_000e6);
        vault.setTokenConfig(address(wbtc), true, 1e8, 10e8);
        vault.setTokenConfig(address(usdt), true, 25_000e6, 250_000e6);

        usdc.mint(deployer, 1_000_000e6);
        wbtc.mint(deployer, 100e8);
        usdt.mint(deployer, 1_000_000e6);

        vm.stopBroadcast();

        console2.log("EscrowVault:", address(vault));
        console2.log("MockUSDC:", address(usdc));
        console2.log("MockWBTC:", address(wbtc));
        console2.log("MockUSDT:", address(usdt));
    }
}
