// SPDX-License-Identifier: MIT
pragma solidity 0.8.34;

/**
 * @title ForecastCommitReveal
 * @notice No-value testnet registry that timestamps forecast commitments and later reveals their preimages.
 * @dev A successful reveal proves only that the caller knew the committed preimage before resolution. It does not
 *      verify a market outcome, forecast quality, or settlement eligibility and never accepts or transfers value.
 * @custom:security-contact security@bellwether.invalid
 */
contract ForecastCommitReveal {
    struct Commitment {
        bytes32 commitmentHash;
        uint256 commitBlock;
        uint64 commitTimestamp;
        uint64 resolutionTime;
        bool revealed;
    }

    mapping(address forecaster => mapping(bytes32 marketId => Commitment commitment)) private s_commitments;

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

    error ForecastCommitReveal__ZeroMarketId();
    error ForecastCommitReveal__ZeroCommitment();
    error ForecastCommitReveal__ZeroModelRunId();
    error ForecastCommitReveal__InvalidResolutionTime(uint64 resolutionTime);
    error ForecastCommitReveal__CommitmentAlreadyExists(address forecaster, bytes32 marketId);
    error ForecastCommitReveal__CommitmentNotFound(address forecaster, bytes32 marketId);
    error ForecastCommitReveal__ProbabilityOutOfRange(uint16 probabilityBps);
    error ForecastCommitReveal__AlreadyRevealed(address forecaster, bytes32 marketId);
    error ForecastCommitReveal__RevealTooEarly(uint64 resolutionTime);
    error ForecastCommitReveal__CommitmentMismatch();

    /*//////////////////////////////////////////////////////////////
                    USER-FACING STATE-CHANGING FUNCTIONS
    //////////////////////////////////////////////////////////////*/

    function commitForecast(bytes32 marketId, bytes32 commitmentHash, uint64 resolutionTime) external {
        if (marketId == bytes32(0)) {
            revert ForecastCommitReveal__ZeroMarketId();
        }
        if (commitmentHash == bytes32(0)) {
            revert ForecastCommitReveal__ZeroCommitment();
        }
        if (resolutionTime <= block.timestamp) {
            revert ForecastCommitReveal__InvalidResolutionTime(resolutionTime);
        }

        Commitment storage commitment = s_commitments[msg.sender][marketId];
        if (commitment.commitmentHash != bytes32(0)) {
            revert ForecastCommitReveal__CommitmentAlreadyExists(msg.sender, marketId);
        }

        uint64 commitTimestamp = uint64(block.timestamp);
        commitment.commitmentHash = commitmentHash;
        commitment.commitBlock = block.number;
        commitment.commitTimestamp = commitTimestamp;
        commitment.resolutionTime = resolutionTime;

        emit ForecastCommitted(msg.sender, marketId, commitmentHash, resolutionTime, block.number, commitTimestamp);
    }

    function revealForecast(bytes32 marketId, uint16 probabilityBps, bytes32 modelRunId, bytes32 salt) external {
        if (marketId == bytes32(0)) {
            revert ForecastCommitReveal__ZeroMarketId();
        }
        if (probabilityBps > 10_000) {
            revert ForecastCommitReveal__ProbabilityOutOfRange(probabilityBps);
        }
        if (modelRunId == bytes32(0)) {
            revert ForecastCommitReveal__ZeroModelRunId();
        }

        Commitment storage commitment = s_commitments[msg.sender][marketId];
        bytes32 commitmentHash = commitment.commitmentHash;
        if (commitmentHash == bytes32(0)) {
            revert ForecastCommitReveal__CommitmentNotFound(msg.sender, marketId);
        }
        if (commitment.revealed) {
            revert ForecastCommitReveal__AlreadyRevealed(msg.sender, marketId);
        }

        uint64 resolutionTime = commitment.resolutionTime;
        if (block.timestamp < resolutionTime) {
            revert ForecastCommitReveal__RevealTooEarly(resolutionTime);
        }
        if (
            commitmentHash != _computeCommitment(msg.sender, marketId, resolutionTime, probabilityBps, modelRunId, salt)
        ) {
            revert ForecastCommitReveal__CommitmentMismatch();
        }

        commitment.revealed = true;

        emit ForecastRevealed(msg.sender, marketId, modelRunId, probabilityBps, salt, resolutionTime);
    }

    /*//////////////////////////////////////////////////////////////
                      USER-FACING READ-ONLY FUNCTIONS
    //////////////////////////////////////////////////////////////*/

    function computeCommitment(
        address forecaster,
        bytes32 marketId,
        uint64 resolutionTime,
        uint16 probabilityBps,
        bytes32 modelRunId,
        bytes32 salt
    ) external view returns (bytes32 commitmentHash) {
        commitmentHash = _computeCommitment(forecaster, marketId, resolutionTime, probabilityBps, modelRunId, salt);
    }

    function getCommitment(address forecaster, bytes32 marketId)
        external
        view
        returns (
            bytes32 commitmentHash,
            uint256 commitBlock,
            uint64 commitTimestamp,
            uint64 resolutionTime,
            bool revealed
        )
    {
        Commitment storage commitment = s_commitments[forecaster][marketId];
        commitmentHash = commitment.commitmentHash;
        commitBlock = commitment.commitBlock;
        commitTimestamp = commitment.commitTimestamp;
        resolutionTime = commitment.resolutionTime;
        revealed = commitment.revealed;
    }

    /*//////////////////////////////////////////////////////////////
                       INTERNAL READ-ONLY FUNCTIONS
    //////////////////////////////////////////////////////////////*/

    function _computeCommitment(
        address forecaster,
        bytes32 marketId,
        uint64 resolutionTime,
        uint16 probabilityBps,
        bytes32 modelRunId,
        bytes32 salt
    ) internal view returns (bytes32 commitmentHash) {
        commitmentHash = keccak256(
            abi.encode(
                address(this), block.chainid, forecaster, marketId, resolutionTime, probabilityBps, modelRunId, salt
            )
        );
    }
}
