// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import { Script, console2 } from "forge-std/Script.sol";
import { EscrowVault } from "../src/EscrowVault.sol";

contract DeploySepolia is Script {
    function run() external returns (EscrowVault vault) {
        uint256 deployerKey = vm.envUint("DEPLOYER_PRIVATE_KEY");
        address deployer = vm.addr(deployerKey);
        address hotSigner = vm.envAddress("HOT_SIGNER_ADDRESS");
        address coldSigner = vm.envAddress("COLD_SIGNER_ADDRESS");

        address usdc = vm.envOr("USDC_TOKEN_ADDRESS", address(0));
        address wbtc = vm.envOr("WBTC_TOKEN_ADDRESS", address(0));
        address usdt = vm.envOr("USDT_TOKEN_ADDRESS", address(0));

        vm.startBroadcast(deployerKey);

        vault = new EscrowVault(deployer, hotSigner, coldSigner);
        vault.setTokenConfig(address(0), true, 5 ether, 50 ether);

        if (usdc != address(0)) {
            vault.setTokenConfig(usdc, true, 25_000e6, 250_000e6);
        }
        if (wbtc != address(0)) {
            vault.setTokenConfig(wbtc, true, 1e8, 10e8);
        }
        if (usdt != address(0)) {
            vault.setTokenConfig(usdt, true, 25_000e6, 250_000e6);
        }

        vm.stopBroadcast();

        console2.log("Sepolia EscrowVault:", address(vault));
    }
}
