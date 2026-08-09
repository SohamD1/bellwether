package snapshot

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"math"

	"github.com/SohamD1/bellwether/internal/features"
)

// snapshotEncodingVersion domains the canonical encoding so later versions
// cannot silently collide with version one IDs.
const snapshotEncodingVersion = "bellwether-feature-snapshot\x00\x01"

// ComputeID returns the lowercase hexadecimal SHA-256 of validated logical
// rows. The canonical version-one encoding is:
//
//   - the bytes of snapshotEncodingVersion;
//   - row count as one big-endian uint64;
//   - for each row: market ID byte length as a big-endian uint64, market ID
//     bytes, block number and log index as big-endian uint64 values, both
//     timestamps as signed Unix nanoseconds encoded in big-endian two's
//     complement uint64 form, finality as one byte, the four float features as
//     big-endian math.Float64bits values, and block lag as big-endian uint64.
//
// Parquet bytes, Parquet metadata, file names, and filesystem metadata are not
// part of the ID.
func ComputeID(rows []features.Snapshot) (string, error) {
	if err := validate(rows); err != nil {
		return "", err
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte(snapshotEncodingVersion))
	writeUint64(digest, uint64(len(rows)))
	for i := range rows {
		row := rows[i]
		writeUint64(digest, uint64(len(row.MarketID)))
		_, _ = digest.Write([]byte(row.MarketID))
		writeUint64(digest, row.BlockNumber)
		writeUint64(digest, row.LogIndex)
		writeUint64(digest, uint64(row.BlockTimestamp.UnixNano()))
		writeUint64(digest, uint64(row.ResolutionTime.UnixNano()))
		_, _ = digest.Write([]byte{byte(row.Finality)})
		writeUint64(digest, math.Float64bits(row.YesImpliedProbability))
		writeUint64(digest, math.Float64bits(row.RecentTradeFlowImbalance))
		writeUint64(digest, math.Float64bits(row.LiquidityDepth))
		writeUint64(digest, math.Float64bits(row.SecondsToResolution))
		writeUint64(digest, row.BlockLag)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func writeUint64(dst hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = dst.Write(encoded[:])
}
