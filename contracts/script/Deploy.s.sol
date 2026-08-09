// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {Script} from "forge-std/Script.sol";
import {BellwetherMarketFixture} from "src/BellwetherMarketFixture.sol";
import {ForecastCommitReveal} from "src/ForecastCommitReveal.sol";

/**
 * @title Deploy
 * @notice Deploys the two no-value Bellwether Base Sepolia demo contracts.
 * @custom:security-contact security@bellwether.invalid
 */
contract Deploy is Script {
    function run()
        external
        returns (BellwetherMarketFixture fixture, ForecastCommitReveal forecastRegistry)
    {
        vm.startBroadcast();
        fixture = new BellwetherMarketFixture();
        forecastRegistry = new ForecastCommitReveal();
        vm.stopBroadcast();
    }
}
