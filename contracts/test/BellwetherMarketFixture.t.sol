// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {Test} from "forge-std/Test.sol";
import {BellwetherMarketFixture} from "src/BellwetherMarketFixture.sol";

contract BellwetherMarketFixtureTest is Test {
    bytes32 internal constant MARKET_ID = keccak256("bellwether-market");
    uint64 internal constant START_TIME = 1_700_000_000;
    uint64 internal constant RESOLUTION_TIME = START_TIME + 1 days;
    uint256 internal constant YES_RESERVE = 1_000 ether;
    uint256 internal constant NO_RESERVE = 2_000 ether;

    BellwetherMarketFixture internal fixture;

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

    function setUp() external {
        vm.warp(START_TIME);
        fixture = new BellwetherMarketFixture();
    }

    function test_CreateMarket_StoresMarketAndEmitsInitialLiquidity() external {
        vm.expectEmit(true, false, false, true, address(fixture));
        emit LiquidityChanged(MARKET_ID, YES_RESERVE, NO_RESERVE, RESOLUTION_TIME);

        fixture.createMarket(MARKET_ID, RESOLUTION_TIME, YES_RESERVE, NO_RESERVE);

        (bool exists, uint64 resolutionTime, uint256 yesReserve, uint256 noReserve, bool resolved, bool outcome) =
            fixture.markets(MARKET_ID);
        assertTrue(exists);
        assertEq(resolutionTime, RESOLUTION_TIME);
        assertEq(yesReserve, YES_RESERVE);
        assertEq(noReserve, NO_RESERVE);
        assertFalse(resolved);
        assertFalse(outcome);
    }

    function testFuzz_CreateMarket_StoresLargeValidReserves(uint128 yesTail, uint128 noTail) external {
        uint256 yesReserve = uint256(type(uint64).max) + 1 + uint256(yesTail);
        uint256 noReserve = uint256(type(uint64).max) + 1 + uint256(noTail);

        fixture.createMarket(MARKET_ID, RESOLUTION_TIME, yesReserve, noReserve);

        (,, uint256 storedYesReserve, uint256 storedNoReserve,,) = fixture.markets(MARKET_ID);
        assertEq(storedYesReserve, yesReserve);
        assertEq(storedNoReserve, noReserve);
    }

    function test_CreateMarket_RevertsWhenMarketIdIsZero() external {
        vm.expectRevert(BellwetherMarketFixture.BellwetherMarketFixture__ZeroMarketId.selector);
        fixture.createMarket(bytes32(0), RESOLUTION_TIME, YES_RESERVE, NO_RESERVE);
    }

    function test_CreateMarket_RevertsWhenYesReserveIsZero() external {
        vm.expectRevert(BellwetherMarketFixture.BellwetherMarketFixture__ZeroReserve.selector);
        fixture.createMarket(MARKET_ID, RESOLUTION_TIME, 0, NO_RESERVE);
    }

    function test_CreateMarket_RevertsWhenNoReserveIsZero() external {
        vm.expectRevert(BellwetherMarketFixture.BellwetherMarketFixture__ZeroReserve.selector);
        fixture.createMarket(MARKET_ID, RESOLUTION_TIME, YES_RESERVE, 0);
    }

    function test_CreateMarket_RevertsWhenResolutionTimeIsNow() external {
        vm.expectRevert(BellwetherMarketFixture.BellwetherMarketFixture__InvalidResolutionTime.selector);
        fixture.createMarket(MARKET_ID, START_TIME, YES_RESERVE, NO_RESERVE);
    }

    function test_CreateMarket_RevertsWhenResolutionTimeIsInPast() external {
        vm.expectRevert(BellwetherMarketFixture.BellwetherMarketFixture__InvalidResolutionTime.selector);
        fixture.createMarket(MARKET_ID, START_TIME - 1, YES_RESERVE, NO_RESERVE);
    }

    function test_CreateMarket_RevertsWhenMarketAlreadyExists() external {
        _createDefaultMarket();

        vm.expectRevert(
            abi.encodeWithSelector(
                BellwetherMarketFixture.BellwetherMarketFixture__MarketAlreadyExists.selector, MARKET_ID
            )
        );
        fixture.createMarket(MARKET_ID, RESOLUTION_TIME + 1, YES_RESERVE, NO_RESERVE);
    }

    function test_SetLiquidity_StoresReservesAndEmitsExactFields() external {
        _createDefaultMarket();
        uint256 newYesReserve = 3_000 ether;
        uint256 newNoReserve = 4_000 ether;

        vm.expectEmit(true, false, false, true, address(fixture));
        emit LiquidityChanged(MARKET_ID, newYesReserve, newNoReserve, RESOLUTION_TIME);
        fixture.setLiquidity(MARKET_ID, newYesReserve, newNoReserve);

        (,, uint256 storedYesReserve, uint256 storedNoReserve,,) = fixture.markets(MARKET_ID);
        assertEq(storedYesReserve, newYesReserve);
        assertEq(storedNoReserve, newNoReserve);
    }

    function test_SetLiquidity_RevertsWhenYesReserveIsZero() external {
        _createDefaultMarket();
        vm.expectRevert(BellwetherMarketFixture.BellwetherMarketFixture__ZeroReserve.selector);
        fixture.setLiquidity(MARKET_ID, 0, NO_RESERVE);
    }

    function test_SetLiquidity_RevertsWhenMarketIdIsZero() external {
        vm.expectRevert(BellwetherMarketFixture.BellwetherMarketFixture__ZeroMarketId.selector);
        fixture.setLiquidity(bytes32(0), YES_RESERVE, NO_RESERVE);
    }

    function test_SetLiquidity_RevertsWhenNoReserveIsZero() external {
        _createDefaultMarket();
        vm.expectRevert(BellwetherMarketFixture.BellwetherMarketFixture__ZeroReserve.selector);
        fixture.setLiquidity(MARKET_ID, YES_RESERVE, 0);
    }

    function test_SetLiquidity_RevertsWhenMarketDoesNotExist() external {
        vm.expectRevert(
            abi.encodeWithSelector(BellwetherMarketFixture.BellwetherMarketFixture__MarketNotFound.selector, MARKET_ID)
        );
        fixture.setLiquidity(MARKET_ID, YES_RESERVE, NO_RESERVE);
    }

    function test_SetLiquidity_RevertsWhenMarketIsResolved() external {
        _createAndResolveDefaultMarket(true);
        vm.expectRevert(
            abi.encodeWithSelector(
                BellwetherMarketFixture.BellwetherMarketFixture__MarketAlreadyResolved.selector, MARKET_ID
            )
        );
        fixture.setLiquidity(MARKET_ID, YES_RESERVE, NO_RESERVE);
    }

    function test_SetLiquidity_RevertsWhenResolutionTimeHasArrived() external {
        _createDefaultMarket();
        vm.warp(RESOLUTION_TIME);
        vm.expectRevert(
            abi.encodeWithSelector(BellwetherMarketFixture.BellwetherMarketFixture__MarketClosed.selector, MARKET_ID)
        );
        fixture.setLiquidity(MARKET_ID, YES_RESERVE, NO_RESERVE);
    }

    function test_RecordTrade_StoresReservesAndEmitsExactFields() external {
        _createDefaultMarket();
        address trader = makeAddr("trader");
        uint256 amount = 12 ether;
        uint256 newYesReserve = 1_012 ether;
        uint256 newNoReserve = 1_976 ether;

        vm.expectEmit(true, true, false, true, address(fixture));
        emit Trade(MARKET_ID, trader, true, amount, newYesReserve, newNoReserve, RESOLUTION_TIME);
        vm.prank(trader);
        fixture.recordTrade(MARKET_ID, true, amount, newYesReserve, newNoReserve);

        (,, uint256 storedYesReserve, uint256 storedNoReserve,,) = fixture.markets(MARKET_ID);
        assertEq(storedYesReserve, newYesReserve);
        assertEq(storedNoReserve, newNoReserve);
    }

    function testFuzz_RecordTrade_PreservesLargeValidValues(uint128 amountTail, uint128 yesTail, uint128 noTail)
        external
    {
        _createDefaultMarket();
        uint256 amount = uint256(type(uint64).max) + 1 + uint256(amountTail);
        uint256 yesReserve = uint256(type(uint64).max) + 1 + uint256(yesTail);
        uint256 noReserve = uint256(type(uint64).max) + 1 + uint256(noTail);
        address trader = makeAddr("large-value-trader");

        vm.expectEmit(true, true, false, true, address(fixture));
        emit Trade(MARKET_ID, trader, false, amount, yesReserve, noReserve, RESOLUTION_TIME);
        vm.prank(trader);
        fixture.recordTrade(MARKET_ID, false, amount, yesReserve, noReserve);

        (,, uint256 storedYesReserve, uint256 storedNoReserve,,) = fixture.markets(MARKET_ID);
        assertEq(storedYesReserve, yesReserve);
        assertEq(storedNoReserve, noReserve);
    }

    function test_RecordTrade_RevertsWhenAmountIsZero() external {
        _createDefaultMarket();
        vm.expectRevert(BellwetherMarketFixture.BellwetherMarketFixture__ZeroAmount.selector);
        fixture.recordTrade(MARKET_ID, true, 0, YES_RESERVE, NO_RESERVE);
    }

    function test_RecordTrade_RevertsWhenMarketIdIsZero() external {
        vm.expectRevert(BellwetherMarketFixture.BellwetherMarketFixture__ZeroMarketId.selector);
        fixture.recordTrade(bytes32(0), true, 1, YES_RESERVE, NO_RESERVE);
    }

    function test_RecordTrade_RevertsWhenYesReserveIsZero() external {
        _createDefaultMarket();
        vm.expectRevert(BellwetherMarketFixture.BellwetherMarketFixture__ZeroReserve.selector);
        fixture.recordTrade(MARKET_ID, true, 1, 0, NO_RESERVE);
    }

    function test_RecordTrade_RevertsWhenNoReserveIsZero() external {
        _createDefaultMarket();
        vm.expectRevert(BellwetherMarketFixture.BellwetherMarketFixture__ZeroReserve.selector);
        fixture.recordTrade(MARKET_ID, true, 1, YES_RESERVE, 0);
    }

    function test_RecordTrade_RevertsWhenMarketDoesNotExist() external {
        vm.expectRevert(
            abi.encodeWithSelector(BellwetherMarketFixture.BellwetherMarketFixture__MarketNotFound.selector, MARKET_ID)
        );
        fixture.recordTrade(MARKET_ID, true, 1, YES_RESERVE, NO_RESERVE);
    }

    function test_RecordTrade_RevertsWhenMarketIsResolved() external {
        _createAndResolveDefaultMarket(false);
        vm.expectRevert(
            abi.encodeWithSelector(
                BellwetherMarketFixture.BellwetherMarketFixture__MarketAlreadyResolved.selector, MARKET_ID
            )
        );
        fixture.recordTrade(MARKET_ID, true, 1, YES_RESERVE, NO_RESERVE);
    }

    function test_RecordTrade_RevertsWhenResolutionTimeHasArrived() external {
        _createDefaultMarket();
        vm.warp(RESOLUTION_TIME);
        vm.expectRevert(
            abi.encodeWithSelector(BellwetherMarketFixture.BellwetherMarketFixture__MarketClosed.selector, MARKET_ID)
        );
        fixture.recordTrade(MARKET_ID, true, 1, YES_RESERVE, NO_RESERVE);
    }

    function test_ResolveMarket_StoresOutcomeAndEmitsExactFields() external {
        _createDefaultMarket();
        vm.warp(RESOLUTION_TIME);

        vm.expectEmit(true, false, false, true, address(fixture));
        emit MarketResolved(MARKET_ID, true);
        fixture.resolveMarket(MARKET_ID, true);

        (,,,, bool resolved, bool outcome) = fixture.markets(MARKET_ID);
        assertTrue(resolved);
        assertTrue(outcome);
    }

    function test_ResolveMarket_RevertsWhenMarketDoesNotExist() external {
        vm.expectRevert(
            abi.encodeWithSelector(BellwetherMarketFixture.BellwetherMarketFixture__MarketNotFound.selector, MARKET_ID)
        );
        fixture.resolveMarket(MARKET_ID, true);
    }

    function test_ResolveMarket_RevertsWhenMarketIdIsZero() external {
        vm.expectRevert(BellwetherMarketFixture.BellwetherMarketFixture__ZeroMarketId.selector);
        fixture.resolveMarket(bytes32(0), true);
    }

    function test_ResolveMarket_RevertsWhenMarketIsAlreadyResolved() external {
        _createAndResolveDefaultMarket(true);
        vm.expectRevert(
            abi.encodeWithSelector(
                BellwetherMarketFixture.BellwetherMarketFixture__MarketAlreadyResolved.selector, MARKET_ID
            )
        );
        fixture.resolveMarket(MARKET_ID, false);
    }

    function test_ResolveMarket_RevertsBeforeResolutionTime() external {
        _createDefaultMarket();
        vm.expectRevert(
            abi.encodeWithSelector(
                BellwetherMarketFixture.BellwetherMarketFixture__ResolutionPending.selector, MARKET_ID
            )
        );
        fixture.resolveMarket(MARKET_ID, true);
    }

    function test_ValueTransfersAreRejectedByEverySurface() external {
        _createDefaultMarket();
        vm.deal(address(this), 5);

        (bool emptyCallAccepted,) = address(fixture).call{value: 1}("");
        (bool createAccepted,) = address(fixture).call{value: 1}(
            abi.encodeCall(
                BellwetherMarketFixture.createMarket,
                (keccak256("other-market"), RESOLUTION_TIME, YES_RESERVE, NO_RESERVE)
            )
        );
        (bool liquidityAccepted,) = address(fixture).call{value: 1}(
            abi.encodeCall(BellwetherMarketFixture.setLiquidity, (MARKET_ID, YES_RESERVE, NO_RESERVE))
        );
        (bool tradeAccepted,) = address(fixture).call{value: 1}(
            abi.encodeCall(BellwetherMarketFixture.recordTrade, (MARKET_ID, true, 1, YES_RESERVE, NO_RESERVE))
        );
        vm.warp(RESOLUTION_TIME);
        (bool resolveAccepted,) =
            address(fixture).call{value: 1}(abi.encodeCall(BellwetherMarketFixture.resolveMarket, (MARKET_ID, true)));

        assertFalse(emptyCallAccepted);
        assertFalse(createAccepted);
        assertFalse(liquidityAccepted);
        assertFalse(tradeAccepted);
        assertFalse(resolveAccepted);
        assertEq(address(fixture).balance, 0);
    }

    function _createDefaultMarket() internal {
        fixture.createMarket(MARKET_ID, RESOLUTION_TIME, YES_RESERVE, NO_RESERVE);
    }

    function _createAndResolveDefaultMarket(bool outcome) internal {
        _createDefaultMarket();
        vm.warp(RESOLUTION_TIME);
        fixture.resolveMarket(MARKET_ID, outcome);
    }
}
