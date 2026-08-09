// SPDX-License-Identifier: MIT
pragma solidity ^0.8.34;

import {Test} from "forge-std/Test.sol";
import {ForecastCommitReveal} from "src/ForecastCommitReveal.sol";

contract ForecastCommitRevealTest is Test {
    bytes32 internal constant MARKET_ID = keccak256("bellwether-market");
    bytes32 internal constant OTHER_MARKET_ID = keccak256("other-market");
    bytes32 internal constant MODEL_RUN_ID = keccak256("model-run");
    bytes32 internal constant SALT = keccak256("forecast-salt");
    uint64 internal constant START_TIME = 1_700_000_000;
    uint64 internal constant RESOLUTION_TIME = START_TIME + 1 days;
    uint16 internal constant PROBABILITY_BPS = 6_250;
    address internal constant FORECASTER = address(0xA11CE);
    address internal constant OTHER_FORECASTER = address(0xB0B);

    ForecastCommitReveal internal registry;

    event ForecastCommitted(
        address indexed forecaster,
        bytes32 indexed marketId,
        bytes32 indexed commitmentHash,
        uint64 resolutionTime,
        uint256 commitBlock,
        uint64 commitTimestamp
    );
    event ForecastRevealed(
        address indexed forecaster,
        bytes32 indexed marketId,
        bytes32 indexed modelRunId,
        uint16 probabilityBps,
        bytes32 salt,
        uint64 resolutionTime
    );

    function setUp() external {
        vm.warp(START_TIME);
        vm.roll(12_345);
        registry = new ForecastCommitReveal();
    }

    function test_ComputeCommitment_MatchesAbiEncodedDomain() external view {
        bytes32 actual =
            registry.computeCommitment(FORECASTER, MARKET_ID, RESOLUTION_TIME, PROBABILITY_BPS, MODEL_RUN_ID, SALT);
        bytes32 expected = keccak256(
            abi.encode(
                address(registry),
                block.chainid,
                FORECASTER,
                MARKET_ID,
                RESOLUTION_TIME,
                PROBABILITY_BPS,
                MODEL_RUN_ID,
                SALT
            )
        );

        assertEq(actual, expected);
    }

    function test_ComputeCommitment_IsBoundToRegistryAddress() external {
        ForecastCommitReveal otherRegistry = new ForecastCommitReveal();
        bytes32 first =
            registry.computeCommitment(FORECASTER, MARKET_ID, RESOLUTION_TIME, PROBABILITY_BPS, MODEL_RUN_ID, SALT);
        bytes32 second = otherRegistry.computeCommitment(
            FORECASTER, MARKET_ID, RESOLUTION_TIME, PROBABILITY_BPS, MODEL_RUN_ID, SALT
        );

        assertNotEq(first, second);
    }

    function test_CommitForecast_StoresExactMetadataAndEmitsExactFields() external {
        bytes32 commitmentHash = _defaultHash(FORECASTER);

        vm.expectEmit(true, true, true, true, address(registry));
        emit ForecastCommitted(
            FORECASTER, MARKET_ID, commitmentHash, RESOLUTION_TIME, block.number, uint64(block.timestamp)
        );
        vm.prank(FORECASTER);
        registry.commitForecast(MARKET_ID, commitmentHash, RESOLUTION_TIME);

        (bytes32 storedHash, uint256 commitBlock, uint64 commitTimestamp, uint64 resolutionTime, bool revealed) =
            registry.getCommitment(FORECASTER, MARKET_ID);
        assertEq(storedHash, commitmentHash);
        assertEq(commitBlock, block.number);
        assertEq(commitTimestamp, START_TIME);
        assertEq(resolutionTime, RESOLUTION_TIME);
        assertFalse(revealed);
    }

    function test_CommitForecast_AllowsLastFutureSecond() external {
        uint64 resolutionTime = START_TIME + 1;
        bytes32 commitmentHash =
            registry.computeCommitment(FORECASTER, MARKET_ID, resolutionTime, PROBABILITY_BPS, MODEL_RUN_ID, SALT);

        vm.prank(FORECASTER);
        registry.commitForecast(MARKET_ID, commitmentHash, resolutionTime);

        (bytes32 storedHash,,, uint64 storedResolutionTime,) = registry.getCommitment(FORECASTER, MARKET_ID);
        assertEq(storedHash, commitmentHash);
        assertEq(storedResolutionTime, resolutionTime);
    }

    function test_CommitForecast_AllowsDifferentCallersForSameMarket() external {
        bytes32 firstHash = _defaultHash(FORECASTER);
        bytes32 secondHash = _defaultHash(OTHER_FORECASTER);

        vm.prank(FORECASTER);
        registry.commitForecast(MARKET_ID, firstHash, RESOLUTION_TIME);
        vm.prank(OTHER_FORECASTER);
        registry.commitForecast(MARKET_ID, secondHash, RESOLUTION_TIME);

        (bytes32 storedFirst,,,,) = registry.getCommitment(FORECASTER, MARKET_ID);
        (bytes32 storedSecond,,,,) = registry.getCommitment(OTHER_FORECASTER, MARKET_ID);
        assertEq(storedFirst, firstHash);
        assertEq(storedSecond, secondHash);
    }

    function test_CommitForecast_RevertsWhenMarketIdIsZero() external {
        vm.expectRevert(ForecastCommitReveal.ForecastCommitReveal__ZeroMarketId.selector);
        registry.commitForecast(bytes32(0), bytes32(uint256(1)), RESOLUTION_TIME);
    }

    function test_CommitForecast_RevertsWhenCommitmentIsZero() external {
        vm.expectRevert(ForecastCommitReveal.ForecastCommitReveal__ZeroCommitment.selector);
        registry.commitForecast(MARKET_ID, bytes32(0), RESOLUTION_TIME);
    }

    function test_CommitForecast_RevertsWhenResolutionTimeIsNow() external {
        vm.expectRevert(
            abi.encodeWithSelector(
                ForecastCommitReveal.ForecastCommitReveal__InvalidResolutionTime.selector, START_TIME
            )
        );
        registry.commitForecast(MARKET_ID, bytes32(uint256(1)), START_TIME);
    }

    function test_CommitForecast_RevertsWhenResolutionTimeIsInPast() external {
        uint64 pastTime = START_TIME - 1;
        vm.expectRevert(
            abi.encodeWithSelector(ForecastCommitReveal.ForecastCommitReveal__InvalidResolutionTime.selector, pastTime)
        );
        registry.commitForecast(MARKET_ID, bytes32(uint256(1)), pastTime);
    }

    function test_CommitForecast_RevertsWhenCommitmentAlreadyExists() external {
        _commitDefault(FORECASTER);

        vm.expectRevert(
            abi.encodeWithSelector(
                ForecastCommitReveal.ForecastCommitReveal__CommitmentAlreadyExists.selector, FORECASTER, MARKET_ID
            )
        );
        vm.prank(FORECASTER);
        registry.commitForecast(MARKET_ID, bytes32(uint256(2)), RESOLUTION_TIME + 1);
    }

    function test_RevealForecast_StoresRevealStateAndEmitsExactFieldsAtBoundary() external {
        bytes32 commitmentHash = _commitDefault(FORECASTER);
        vm.warp(RESOLUTION_TIME);

        vm.expectEmit(true, true, true, true, address(registry));
        emit ForecastRevealed(FORECASTER, MARKET_ID, MODEL_RUN_ID, PROBABILITY_BPS, SALT, RESOLUTION_TIME);
        vm.prank(FORECASTER);
        registry.revealForecast(MARKET_ID, PROBABILITY_BPS, MODEL_RUN_ID, SALT);

        (bytes32 storedHash, uint256 commitBlock, uint64 commitTimestamp, uint64 resolutionTime, bool revealed) =
            registry.getCommitment(FORECASTER, MARKET_ID);
        assertEq(storedHash, commitmentHash);
        assertEq(commitBlock, 12_345);
        assertEq(commitTimestamp, START_TIME);
        assertEq(resolutionTime, RESOLUTION_TIME);
        assertTrue(revealed);
    }

    function test_RevealForecast_RevertsOneSecondBeforeResolution() external {
        _commitDefault(FORECASTER);
        vm.warp(RESOLUTION_TIME - 1);

        vm.expectRevert(
            abi.encodeWithSelector(ForecastCommitReveal.ForecastCommitReveal__RevealTooEarly.selector, RESOLUTION_TIME)
        );
        vm.prank(FORECASTER);
        registry.revealForecast(MARKET_ID, PROBABILITY_BPS, MODEL_RUN_ID, SALT);
    }

    function test_RevealForecast_RevertsWhenMarketIdIsZero() external {
        vm.expectRevert(ForecastCommitReveal.ForecastCommitReveal__ZeroMarketId.selector);
        registry.revealForecast(bytes32(0), PROBABILITY_BPS, MODEL_RUN_ID, SALT);
    }

    function test_RevealForecast_RevertsWhenCommitmentIsUnknown() external {
        vm.expectRevert(
            abi.encodeWithSelector(
                ForecastCommitReveal.ForecastCommitReveal__CommitmentNotFound.selector, FORECASTER, MARKET_ID
            )
        );
        vm.prank(FORECASTER);
        registry.revealForecast(MARKET_ID, PROBABILITY_BPS, MODEL_RUN_ID, SALT);
    }

    function test_RevealForecast_RevertsForAnotherCaller() external {
        _commitDefault(FORECASTER);
        vm.warp(RESOLUTION_TIME);

        vm.expectRevert(
            abi.encodeWithSelector(
                ForecastCommitReveal.ForecastCommitReveal__CommitmentNotFound.selector, OTHER_FORECASTER, MARKET_ID
            )
        );
        vm.prank(OTHER_FORECASTER);
        registry.revealForecast(MARKET_ID, PROBABILITY_BPS, MODEL_RUN_ID, SALT);
    }

    function test_RevealForecast_RevertsWhenProbabilityExceedsTenThousandBps() external {
        uint16 invalidProbabilityBps = 10_001;
        _commitDefault(FORECASTER);
        vm.warp(RESOLUTION_TIME);

        vm.expectRevert(
            abi.encodeWithSelector(
                ForecastCommitReveal.ForecastCommitReveal__ProbabilityOutOfRange.selector, invalidProbabilityBps
            )
        );
        vm.prank(FORECASTER);
        registry.revealForecast(MARKET_ID, invalidProbabilityBps, MODEL_RUN_ID, SALT);
    }

    function test_RevealForecast_RevertsWhenSaltIsWrong() external {
        _commitDefault(FORECASTER);
        vm.warp(RESOLUTION_TIME);

        vm.expectRevert(ForecastCommitReveal.ForecastCommitReveal__CommitmentMismatch.selector);
        vm.prank(FORECASTER);
        registry.revealForecast(MARKET_ID, PROBABILITY_BPS, MODEL_RUN_ID, keccak256("wrong-salt"));
    }

    function test_RevealForecast_RevertsWhenModelRunIdIsWrong() external {
        _commitDefault(FORECASTER);
        vm.warp(RESOLUTION_TIME);

        vm.expectRevert(ForecastCommitReveal.ForecastCommitReveal__CommitmentMismatch.selector);
        vm.prank(FORECASTER);
        registry.revealForecast(MARKET_ID, PROBABILITY_BPS, keccak256("wrong-run"), SALT);
    }

    function test_RevealForecast_RevertsWhenProbabilityIsWrong() external {
        _commitDefault(FORECASTER);
        vm.warp(RESOLUTION_TIME);

        vm.expectRevert(ForecastCommitReveal.ForecastCommitReveal__CommitmentMismatch.selector);
        vm.prank(FORECASTER);
        registry.revealForecast(MARKET_ID, PROBABILITY_BPS + 1, MODEL_RUN_ID, SALT);
    }

    function test_RevealForecast_RevertsWhenCommittedResolutionPreimageWasWrong() external {
        bytes32 commitmentHash =
            registry.computeCommitment(FORECASTER, MARKET_ID, RESOLUTION_TIME + 1, PROBABILITY_BPS, MODEL_RUN_ID, SALT);
        vm.prank(FORECASTER);
        registry.commitForecast(MARKET_ID, commitmentHash, RESOLUTION_TIME);
        vm.warp(RESOLUTION_TIME);

        vm.expectRevert(ForecastCommitReveal.ForecastCommitReveal__CommitmentMismatch.selector);
        vm.prank(FORECASTER);
        registry.revealForecast(MARKET_ID, PROBABILITY_BPS, MODEL_RUN_ID, SALT);
    }

    function test_RevealForecast_RevertsWhenMarketIdDoesNotMatch() external {
        _commitDefault(FORECASTER);
        vm.warp(RESOLUTION_TIME);

        vm.expectRevert(
            abi.encodeWithSelector(
                ForecastCommitReveal.ForecastCommitReveal__CommitmentNotFound.selector, FORECASTER, OTHER_MARKET_ID
            )
        );
        vm.prank(FORECASTER);
        registry.revealForecast(OTHER_MARKET_ID, PROBABILITY_BPS, MODEL_RUN_ID, SALT);
    }

    function test_RevealForecast_RevertsWhenAlreadyRevealed() external {
        _commitDefault(FORECASTER);
        vm.warp(RESOLUTION_TIME);
        vm.prank(FORECASTER);
        registry.revealForecast(MARKET_ID, PROBABILITY_BPS, MODEL_RUN_ID, SALT);

        vm.expectRevert(
            abi.encodeWithSelector(
                ForecastCommitReveal.ForecastCommitReveal__AlreadyRevealed.selector, FORECASTER, MARKET_ID
            )
        );
        vm.prank(FORECASTER);
        registry.revealForecast(MARKET_ID, PROBABILITY_BPS, MODEL_RUN_ID, SALT);
    }

    function testFuzz_RevealForecast_AcceptsValidProbabilityAndSalt(uint16 probabilityBps, bytes32 salt) external {
        probabilityBps = uint16(bound(probabilityBps, 0, 10_000));
        bytes32 commitmentHash =
            registry.computeCommitment(FORECASTER, MARKET_ID, RESOLUTION_TIME, probabilityBps, MODEL_RUN_ID, salt);
        vm.prank(FORECASTER);
        registry.commitForecast(MARKET_ID, commitmentHash, RESOLUTION_TIME);
        vm.warp(RESOLUTION_TIME);

        vm.prank(FORECASTER);
        registry.revealForecast(MARKET_ID, probabilityBps, MODEL_RUN_ID, salt);

        (,,,, bool revealed) = registry.getCommitment(FORECASTER, MARKET_ID);
        assertTrue(revealed);
    }

    function testFuzz_RevealForecast_RejectsCorruptedSalt(bytes32 corruption) external {
        vm.assume(corruption != bytes32(0));
        _commitDefault(FORECASTER);
        vm.warp(RESOLUTION_TIME);

        vm.expectRevert(ForecastCommitReveal.ForecastCommitReveal__CommitmentMismatch.selector);
        vm.prank(FORECASTER);
        registry.revealForecast(MARKET_ID, PROBABILITY_BPS, MODEL_RUN_ID, SALT ^ corruption);
    }

    function testFuzz_RevealForecast_RejectsCorruptedModelRunId(bytes32 corruption) external {
        vm.assume(corruption != bytes32(0));
        _commitDefault(FORECASTER);
        vm.warp(RESOLUTION_TIME);

        vm.expectRevert(ForecastCommitReveal.ForecastCommitReveal__CommitmentMismatch.selector);
        vm.prank(FORECASTER);
        registry.revealForecast(MARKET_ID, PROBABILITY_BPS, MODEL_RUN_ID ^ corruption, SALT);
    }

    function test_ValueTransfersAndEmptyCalldataAreRejectedByEverySurface() external {
        vm.deal(address(this), 6);
        bytes32 commitmentHash = _defaultHash(address(this));

        (bool emptyAccepted,) = address(registry).call{value: 1}("");
        (bool commitAccepted,) = address(registry).call{value: 1}(
            abi.encodeCall(ForecastCommitReveal.commitForecast, (MARKET_ID, commitmentHash, RESOLUTION_TIME))
        );
        (bool revealAccepted,) = address(registry).call{value: 1}(
            abi.encodeCall(ForecastCommitReveal.revealForecast, (MARKET_ID, PROBABILITY_BPS, MODEL_RUN_ID, SALT))
        );
        (bool hashAccepted,) = address(registry).call{value: 1}(
            abi.encodeCall(
                ForecastCommitReveal.computeCommitment,
                (address(this), MARKET_ID, RESOLUTION_TIME, PROBABILITY_BPS, MODEL_RUN_ID, SALT)
            )
        );
        (bool getterAccepted,) = address(registry).call{value: 1}(
            abi.encodeCall(ForecastCommitReveal.getCommitment, (address(this), MARKET_ID))
        );

        assertFalse(emptyAccepted);
        assertFalse(commitAccepted);
        assertFalse(revealAccepted);
        assertFalse(hashAccepted);
        assertFalse(getterAccepted);
        assertEq(address(registry).balance, 0);
    }

    function _defaultHash(address forecaster) internal view returns (bytes32 commitmentHash) {
        commitmentHash =
            registry.computeCommitment(forecaster, MARKET_ID, RESOLUTION_TIME, PROBABILITY_BPS, MODEL_RUN_ID, SALT);
    }

    function _commitDefault(address forecaster) internal returns (bytes32 commitmentHash) {
        commitmentHash = _defaultHash(forecaster);
        vm.prank(forecaster);
        registry.commitForecast(MARKET_ID, commitmentHash, RESOLUTION_TIME);
    }
}
