package trainingdata

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"math"
)

const datasetEncodingVersion = "bellwether-labeled-training-data\x00\x01"

// ComputeID returns a canonical SHA-256 identifier for validated logical rows.
// The encoding is the version domain and row count, followed by each row's
// length-prefixed market ID; positions; signed Unix-nanosecond timestamps in
// two's-complement uint64 form; finality; float64 bits; block lag; and a single
// outcome byte. Every integer is big-endian. Parquet and filesystem metadata
// are deliberately excluded.
func ComputeID(rows []LabeledRow) (string, error) {
	if err := validate(rows); err != nil {
		return "", err
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte(datasetEncodingVersion))
	writeUint64(digest, uint64(len(rows)))
	for i := range rows {
		row := rows[i]
		writeUint64(digest, uint64(len(row.MarketID)))
		_, _ = digest.Write([]byte(row.MarketID))
		writeUint64(digest, row.BlockNumber)
		writeUint64(digest, row.LogIndex)
		writeUint64(digest, uint64(row.BlockTimestamp.UnixNano()))
		writeUint64(digest, uint64(row.ResolutionTime.UnixNano()))
		writeUint64(digest, uint64(row.LabelAvailableAt.UnixNano()))
		writeUint64(digest, uint64(row.Finality))
		writeUint64(digest, math.Float64bits(row.YesImpliedProbability))
		writeUint64(digest, math.Float64bits(row.RecentTradeFlowImbalance))
		writeUint64(digest, math.Float64bits(row.LiquidityDepth))
		writeUint64(digest, math.Float64bits(row.SecondsToResolution))
		writeUint64(digest, row.BlockLag)
		if row.Outcome {
			_, _ = digest.Write([]byte{1})
		} else {
			_, _ = digest.Write([]byte{0})
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func writeUint64(dst hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = dst.Write(encoded[:])
}
