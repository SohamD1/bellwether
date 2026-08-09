// Package inference serves binary LightGBM forecasts in-process from the same
// five point-in-time features used by the Python training harness.
package inference

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/SohamD1/bellwether/internal/features"
	"github.com/dmitryikh/leaves"
	"github.com/dmitryikh/leaves/transformation"
)

const (
	// FeatureCount is the exact number of inputs accepted by Bellwether models.
	FeatureCount    = 5
	maxExactInteger = uint64(1 << 53)
)

var (
	// ErrInvalidModel reports a model that does not match the binary Bellwether
	// schema or cannot be parsed by leaves.
	ErrInvalidModel = errors.New("inference: invalid model")
	// ErrInvalidFeatures reports non-finite or out-of-domain model input.
	ErrInvalidFeatures = errors.New("inference: invalid features")
	// ErrInvalidPrediction reports a non-finite value or a value outside [0, 1].
	ErrInvalidPrediction = errors.New("inference: invalid prediction")
)

var featureColumns = [FeatureCount]string{
	"yes_implied_probability",
	"recent_trade_flow_imbalance",
	"liquidity_depth",
	"seconds_to_resolution",
	"block_lag",
}

type predictor interface {
	Predict([]float64, int, []float64) error
}

// Model is a loaded binary LightGBM ensemble. It is safe for concurrent
// prediction after Load returns because prediction does not mutate the model.
type Model struct {
	ensemble predictor
}

// FeatureColumns returns the required training and serving column order.
func FeatureColumns() [FeatureCount]string {
	return featureColumns
}

// SnapshotVector validates snapshot and converts it to the fixed model order.
// The returned array owns its storage and does not alias snapshot.
func SnapshotVector(snapshot features.Snapshot) ([FeatureCount]float64, error) {
	values := [FeatureCount]float64{
		snapshot.YesImpliedProbability,
		snapshot.RecentTradeFlowImbalance,
		snapshot.LiquidityDepth,
		snapshot.SecondsToResolution,
		float64(snapshot.BlockLag),
	}
	if !finiteBetween(values[0], 0, 1) {
		return [FeatureCount]float64{}, fmt.Errorf("%w: %s must be finite and in [0, 1]", ErrInvalidFeatures, featureColumns[0])
	}
	if !finiteBetween(values[1], -1, 1) {
		return [FeatureCount]float64{}, fmt.Errorf("%w: %s must be finite and in [-1, 1]", ErrInvalidFeatures, featureColumns[1])
	}
	if !finiteNonNegative(values[2]) {
		return [FeatureCount]float64{}, fmt.Errorf("%w: %s must be finite and non-negative", ErrInvalidFeatures, featureColumns[2])
	}
	if !finiteNonNegative(values[3]) {
		return [FeatureCount]float64{}, fmt.Errorf("%w: %s must be finite and non-negative", ErrInvalidFeatures, featureColumns[3])
	}
	if snapshot.BlockLag > maxExactInteger {
		return [FeatureCount]float64{}, fmt.Errorf("%w: %s exceeds exact float64 integer range", ErrInvalidFeatures, featureColumns[4])
	}
	return values, nil
}

// Load reads a LightGBM text model with probability transformation enabled.
// The file must declare the exact five feature names in FeatureColumns order
// and use a single-output binary objective.
func Load(path string) (*Model, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: read %q: %v", ErrInvalidModel, path, err)
	}
	if err := validateHeader(data); err != nil {
		return nil, err
	}
	leavesText, err := leavesCompatibleModelText(data)
	if err != nil {
		return nil, err
	}

	ensemble, err := leaves.LGEnsembleFromReader(bufio.NewReader(bytes.NewReader(leavesText)), true)
	if err != nil {
		return nil, fmt.Errorf("%w: parse LightGBM text: %v", ErrInvalidModel, err)
	}
	if ensemble.NFeatures() != FeatureCount {
		return nil, fmt.Errorf("%w: model has %d features, want %d", ErrInvalidModel, ensemble.NFeatures(), FeatureCount)
	}
	if ensemble.NRawOutputGroups() != 1 || ensemble.NOutputGroups() != 1 || ensemble.Transformation().Type() != transformation.Logistic {
		return nil, fmt.Errorf("%w: model must produce one transformed binary probability", ErrInvalidModel)
	}
	return &Model{ensemble: ensemble}, nil
}

// Predict returns the positive-class probability for snapshot.
func (m *Model) Predict(snapshot features.Snapshot) (float64, error) {
	if m == nil || m.ensemble == nil {
		return 0, fmt.Errorf("%w: model is not loaded", ErrInvalidModel)
	}
	vector, err := SnapshotVector(snapshot)
	if err != nil {
		return 0, err
	}
	output := [1]float64{}
	if err := m.ensemble.Predict(vector[:], 0, output[:]); err != nil {
		return 0, fmt.Errorf("inference: predict: %w", err)
	}
	if !finiteBetween(output[0], 0, 1) {
		return 0, fmt.Errorf("%w: got %v", ErrInvalidPrediction, output[0])
	}
	return output[0], nil
}

// leavesCompatibleModelText adapts the LightGBM 4.7 text header to the latest
// version understood by leaves. LightGBM's v4 tree records used by Bellwether
// remain compatible, so this changes only the exact v4 version line. Unknown
// future versions are rejected instead of being silently reinterpreted.
func leavesCompatibleModelText(data []byte) ([]byte, error) {
	switch {
	case bytes.HasPrefix(data, []byte("tree\nversion=v4\n")):
		return bytes.Replace(data, []byte("\nversion=v4\n"), []byte("\nversion=v3\n"), 1), nil
	case bytes.HasPrefix(data, []byte("tree\nversion=v3\n")),
		bytes.HasPrefix(data, []byte("tree\nversion=v2\n")):
		return data, nil
	default:
		return nil, fmt.Errorf("%w: unsupported LightGBM text version", ErrInvalidModel)
	}
}

func validateHeader(data []byte) error {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	if !scanner.Scan() || scanner.Text() != "tree" {
		return fmt.Errorf("%w: missing LightGBM tree header", ErrInvalidModel)
	}
	params := make(map[string]string)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			return fmt.Errorf("%w: malformed header line %q", ErrInvalidModel, line)
		}
		params[key] = value
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("%w: scan header: %v", ErrInvalidModel, err)
	}

	if params["objective"] != "binary sigmoid:1" {
		return fmt.Errorf("%w: objective must be binary sigmoid:1", ErrInvalidModel)
	}
	if params["num_class"] != "1" || params["num_tree_per_iteration"] != "1" {
		return fmt.Errorf("%w: model must have one output group", ErrInvalidModel)
	}
	maxFeature, err := strconv.Atoi(params["max_feature_idx"])
	if err != nil || maxFeature+1 != FeatureCount {
		return fmt.Errorf("%w: model feature count must be %d", ErrInvalidModel, FeatureCount)
	}
	modelColumns := strings.Fields(params["feature_names"])
	if len(modelColumns) != FeatureCount {
		return fmt.Errorf("%w: model feature names have length %d, want %d", ErrInvalidModel, len(modelColumns), FeatureCount)
	}
	for i, want := range featureColumns {
		if modelColumns[i] != want {
			return fmt.Errorf("%w: feature %d is %q, want %q", ErrInvalidModel, i, modelColumns[i], want)
		}
	}
	return nil
}

func finiteBetween(value, lower, upper float64) bool {
	return value >= lower && value <= upper && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func finiteNonNegative(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
