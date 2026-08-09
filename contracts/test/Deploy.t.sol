// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {Test} from "forge-std/Test.sol";
import {Deploy} from "script/Deploy.s.sol";
import {BellwetherMarketFixture} from "src/BellwetherMarketFixture.sol";
import {ForecastCommitReveal} from "src/ForecastCommitReveal.sol";

contract DeployTest is Test {
    function test_Run_DeploysBothDemoContracts() external {
        Deploy deployer = new Deploy();

        (BellwetherMarketFixture fixture, ForecastCommitReveal forecastRegistry) = deployer.run();

        assertGt(address(fixture).code.length, 0);
        assertGt(address(forecastRegistry).code.length, 0);
    }
}
