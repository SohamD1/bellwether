// SPDX-License-Identifier: MIT
pragma solidity 0.8.34;

/**
 * @title BellwetherMarketFixture
 * @notice No-value event fixture for Bellwether Base Sepolia integration tests.
 * @dev This contract is not an AMM and never accepts or transfers ETH or tokens.
 * @custom:security-contact security@bellwether.invalid
 */
contract BellwetherMarketFixture {
    struct Market {
        uint64 resolutionTime;
        bool exists;
        bool resolved;
        bool outcome;
        uint256 yesReserve;
        uint256 noReserve;
    }

    mapping(bytes32 marketId => Market market) private s_markets;

    event Trade(
        bytes32 indexed marketId,
        address indexed trader,
        bool yes,
        uint256 amount,
        uint256 yesReserve,
        uint256 noReserve,
        uint64 resolutionTime
    );
    event LiquidityChanged(bytes32 indexed marketId, uint256 yesReserve, uint256 noReserve, uint64 resolutionTime);
    event MarketResolved(bytes32 indexed marketId, bool outcome);

    error BellwetherMarketFixture__ZeroMarketId();
    error BellwetherMarketFixture__ZeroReserve();
    error BellwetherMarketFixture__ZeroAmount();
    error BellwetherMarketFixture__InvalidResolutionTime();
    error BellwetherMarketFixture__MarketAlreadyExists(bytes32 marketId);
    error BellwetherMarketFixture__MarketNotFound(bytes32 marketId);
    error BellwetherMarketFixture__MarketAlreadyResolved(bytes32 marketId);
    error BellwetherMarketFixture__MarketClosed(bytes32 marketId);
    error BellwetherMarketFixture__ResolutionPending(bytes32 marketId);

    /*//////////////////////////////////////////////////////////////
                    USER-FACING STATE-CHANGING FUNCTIONS
    //////////////////////////////////////////////////////////////*/

    function createMarket(bytes32 marketId, uint64 resolutionTime, uint256 yesReserve, uint256 noReserve) external {
        if (marketId == bytes32(0)) {
            revert BellwetherMarketFixture__ZeroMarketId();
        }
        if (yesReserve == 0 || noReserve == 0) {
            revert BellwetherMarketFixture__ZeroReserve();
        }
        if (resolutionTime <= block.timestamp) {
            revert BellwetherMarketFixture__InvalidResolutionTime();
        }
        if (s_markets[marketId].exists) {
            revert BellwetherMarketFixture__MarketAlreadyExists(marketId);
        }

        s_markets[marketId] = Market({
            resolutionTime: resolutionTime,
            exists: true,
            resolved: false,
            outcome: false,
            yesReserve: yesReserve,
            noReserve: noReserve
        });

        emit LiquidityChanged(marketId, yesReserve, noReserve, resolutionTime);
    }

    function setLiquidity(bytes32 marketId, uint256 yesReserve, uint256 noReserve) external {
        if (yesReserve == 0 || noReserve == 0) {
            revert BellwetherMarketFixture__ZeroReserve();
        }

        Market storage market = _openMarket(marketId);
        market.yesReserve = yesReserve;
        market.noReserve = noReserve;

        emit LiquidityChanged(marketId, yesReserve, noReserve, market.resolutionTime);
    }

    function recordTrade(bytes32 marketId, bool yes, uint256 amount, uint256 yesReserve, uint256 noReserve) external {
        if (amount == 0) {
            revert BellwetherMarketFixture__ZeroAmount();
        }
        if (yesReserve == 0 || noReserve == 0) {
            revert BellwetherMarketFixture__ZeroReserve();
        }

        Market storage market = _openMarket(marketId);
        market.yesReserve = yesReserve;
        market.noReserve = noReserve;

        emit Trade(marketId, msg.sender, yes, amount, yesReserve, noReserve, market.resolutionTime);
    }

    function resolveMarket(bytes32 marketId, bool outcome) external {
        Market storage market = _existingMarket(marketId);
        if (market.resolved) {
            revert BellwetherMarketFixture__MarketAlreadyResolved(marketId);
        }
        if (block.timestamp < market.resolutionTime) {
            revert BellwetherMarketFixture__ResolutionPending(marketId);
        }

        market.resolved = true;
        market.outcome = outcome;

        emit MarketResolved(marketId, outcome);
    }

    /*//////////////////////////////////////////////////////////////
                      USER-FACING READ-ONLY FUNCTIONS
    //////////////////////////////////////////////////////////////*/

    function markets(bytes32 marketId)
        external
        view
        returns (bool exists, uint64 resolutionTime, uint256 yesReserve, uint256 noReserve, bool resolved, bool outcome)
    {
        Market storage market = s_markets[marketId];
        exists = market.exists;
        resolutionTime = market.resolutionTime;
        yesReserve = market.yesReserve;
        noReserve = market.noReserve;
        resolved = market.resolved;
        outcome = market.outcome;
    }

    /*//////////////////////////////////////////////////////////////
                       INTERNAL READ-ONLY FUNCTIONS
    //////////////////////////////////////////////////////////////*/

    function _openMarket(bytes32 marketId) internal view returns (Market storage market) {
        market = _existingMarket(marketId);
        if (market.resolved) {
            revert BellwetherMarketFixture__MarketAlreadyResolved(marketId);
        }
        if (block.timestamp >= market.resolutionTime) {
            revert BellwetherMarketFixture__MarketClosed(marketId);
        }
    }

    function _existingMarket(bytes32 marketId) internal view returns (Market storage market) {
        if (marketId == bytes32(0)) {
            revert BellwetherMarketFixture__ZeroMarketId();
        }
        market = s_markets[marketId];
        if (!market.exists) {
            revert BellwetherMarketFixture__MarketNotFound(marketId);
        }
    }
}
