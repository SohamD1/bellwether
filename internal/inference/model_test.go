package inference

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SohamD1/bellwether/internal/features"
)

func TestFeatureColumns(t *testing.T) {
	t.Parallel()

	want := [FeatureCount]string{
		"yes_implied_probability",
		"recent_trade_flow_imbalance",
		"liquidity_depth",
		"seconds_to_resolution",
		"block_lag",
	}
	if got := FeatureColumns(); got != want {
		t.Fatalf("FeatureColumns() = %q, want %q", got, want)
	}
}

func TestSnapshotVector(t *testing.T) {
	t.Parallel()

	snapshot := validSnapshot()
	want := [FeatureCount]float64{0.64, -0.25, 1_250, 7_200.5, 3}
	got, err := SnapshotVector(snapshot)
	if err != nil {
		t.Fatalf("SnapshotVector() error = %v", err)
	}
	if got != want {
		t.Fatalf("SnapshotVector() = %v, want %v", got, want)
	}

	got[0] = 0
	again, err := SnapshotVector(snapshot)
	if err != nil {
		t.Fatalf("second SnapshotVector() error = %v", err)
	}
	if again != want {
		t.Fatalf("returned vector aliases later conversion: got %v, want %v", again, want)
	}
	if snapshot != validSnapshot() {
		t.Fatalf("SnapshotVector mutated its input: got %+v", snapshot)
	}
}

func TestSnapshotVectorRejectsInvalidFeatures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*features.Snapshot)
	}{
		{name: "probability NaN", mutate: func(s *features.Snapshot) { s.YesImpliedProbability = math.NaN() }},
		{name: "probability below zero", mutate: func(s *features.Snapshot) { s.YesImpliedProbability = -0.01 }},
		{name: "probability above one", mutate: func(s *features.Snapshot) { s.YesImpliedProbability = 1.01 }},
		{name: "flow infinite", mutate: func(s *features.Snapshot) { s.RecentTradeFlowImbalance = math.Inf(1) }},
		{name: "flow below minus one", mutate: func(s *features.Snapshot) { s.RecentTradeFlowImbalance = -1.01 }},
		{name: "flow above one", mutate: func(s *features.Snapshot) { s.RecentTradeFlowImbalance = 1.01 }},
		{name: "liquidity negative", mutate: func(s *features.Snapshot) { s.LiquidityDepth = -1 }},
		{name: "liquidity infinite", mutate: func(s *features.Snapshot) { s.LiquidityDepth = math.Inf(1) }},
		{name: "seconds negative", mutate: func(s *features.Snapshot) { s.SecondsToResolution = -1 }},
		{name: "seconds NaN", mutate: func(s *features.Snapshot) { s.SecondsToResolution = math.NaN() }},
		{name: "block lag loses integer precision", mutate: func(s *features.Snapshot) { s.BlockLag = 1<<53 + 1 }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			snapshot := validSnapshot()
			test.mutate(&snapshot)
			_, err := SnapshotVector(snapshot)
			if !errors.Is(err, ErrInvalidFeatures) {
				t.Fatalf("SnapshotVector() error = %v, want ErrInvalidFeatures", err)
			}
		})
	}
}

func TestLoadAndPredictMatchesLightGBM47(t *testing.T) {
	t.Parallel()

	fixture := loadCompatibilityFixture(t)
	modelPath := filepath.Join("testdata", "compatibility_model.txt")
	modelText, err := os.ReadFile(modelPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(modelText, []byte("tree\nversion=v4\n")) {
		t.Fatal("compatibility fixture is not raw LightGBM 4.7 version v4 text")
	}
	model, err := Load(modelPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if fixture.LightGBMVersion != "4.7.0" || fixture.TreeCount != 200 {
		t.Fatalf("unexpected compatibility fixture metadata: version=%q trees=%d", fixture.LightGBMVersion, fixture.TreeCount)
	}
	if got := FeatureColumns(); got != fixture.FeatureNames {
		t.Fatalf("fixture feature order = %q, wrapper order = %q", fixture.FeatureNames, got)
	}

	const tolerance = 1e-12
	for i, row := range fixture.Rows {
		snapshot := features.Snapshot{
			YesImpliedProbability:    row[0],
			RecentTradeFlowImbalance: row[1],
			LiquidityDepth:           row[2],
			SecondsToResolution:      row[3],
			BlockLag:                 uint64(row[4]),
		}
		before := snapshot
		got, err := model.Predict(snapshot)
		if err != nil {
			t.Fatalf("Predict(row %d) error = %v", i, err)
		}
		if diff := math.Abs(got - fixture.Probabilities[i]); diff > tolerance {
			t.Fatalf("Predict(row %d) = %.17g, native LightGBM = %.17g, diff %.3g > %.1g", i, got, fixture.Probabilities[i], diff, tolerance)
		}
		if snapshot != before {
			t.Fatalf("Predict(row %d) mutated snapshot: got %+v, want %+v", i, snapshot, before)
		}
	}
}

func TestLoadRejectsIncompatibleModels(t *testing.T) {
	t.Parallel()

	modelText, err := os.ReadFile(filepath.Join("testdata", "compatibility_model.txt"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		modify func(string) string
	}{
		{
			name: "wrong feature count",
			modify: func(model string) string {
				model = strings.Replace(model, "max_feature_idx=4", "max_feature_idx=3", 1)
				return strings.Replace(model, "feature_names=yes_implied_probability recent_trade_flow_imbalance liquidity_depth seconds_to_resolution block_lag", "feature_names=yes_implied_probability recent_trade_flow_imbalance liquidity_depth seconds_to_resolution", 1)
			},
		},
		{
			name: "wrong feature order",
			modify: func(model string) string {
				return strings.Replace(model, "feature_names=yes_implied_probability recent_trade_flow_imbalance", "feature_names=recent_trade_flow_imbalance yes_implied_probability", 1)
			},
		},
		{
			name: "non-binary objective",
			modify: func(model string) string {
				return strings.Replace(model, "objective=binary sigmoid:1", "objective=regression", 1)
			},
		},
		{
			name:   "malformed model",
			modify: func(string) string { return "not a LightGBM model\n" },
		},
		{
			name: "unknown future version",
			modify: func(model string) string {
				return strings.Replace(model, "version=v4", "version=v5", 1)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "model.txt")
			if err := os.WriteFile(path, []byte(test.modify(string(modelText))), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if !errors.Is(err, ErrInvalidModel) {
				t.Fatalf("Load() error = %v, want ErrInvalidModel", err)
			}
		})
	}
}

func TestLeavesCompatibleModelTextOnlyNormalizesV4Header(t *testing.T) {
	t.Parallel()

	raw := []byte("tree\nversion=v4\nnum_class=1\n\nTree=0\nleaf_value=4\n")
	want := []byte("tree\nversion=v3\nnum_class=1\n\nTree=0\nleaf_value=4\n")
	got, err := leavesCompatibleModelText(raw)
	if err != nil {
		t.Fatalf("leavesCompatibleModelText() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("leavesCompatibleModelText() = %q, want %q", got, want)
	}
	if !bytes.Equal(raw, []byte("tree\nversion=v4\nnum_class=1\n\nTree=0\nleaf_value=4\n")) {
		t.Fatalf("leavesCompatibleModelText mutated input: %q", raw)
	}
}

func TestModelPredictRejectsInvalidOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value float64
	}{
		{name: "NaN", value: math.NaN()},
		{name: "positive infinity", value: math.Inf(1)},
		{name: "below zero", value: -0.001},
		{name: "above one", value: 1.001},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			model := &Model{ensemble: fixedPredictor{value: test.value}}
			_, err := model.Predict(validSnapshot())
			if !errors.Is(err, ErrInvalidPrediction) {
				t.Fatalf("Predict() error = %v, want ErrInvalidPrediction", err)
			}
		})
	}
}

func TestModelPredictPropagatesEngineError(t *testing.T) {
	t.Parallel()

	want := errors.New("predict failed")
	model := &Model{ensemble: fixedPredictor{err: want}}
	_, err := model.Predict(validSnapshot())
	if !errors.Is(err, want) {
		t.Fatalf("Predict() error = %v, want wrapped %v", err, want)
	}
}

type compatibilityFixture struct {
	LightGBMVersion string                  `json:"lightgbm_version"`
	TreeCount       int                     `json:"tree_count"`
	FeatureNames    [FeatureCount]string    `json:"feature_names"`
	Rows            [][FeatureCount]float64 `json:"rows"`
	Probabilities   []float64               `json:"probabilities"`
}

func loadCompatibilityFixture(t *testing.T) compatibilityFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "compatibility_expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture compatibilityFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Rows) != len(fixture.Probabilities) {
		t.Fatalf("fixture has %d rows and %d predictions", len(fixture.Rows), len(fixture.Probabilities))
	}
	return fixture
}

func validSnapshot() features.Snapshot {
	return features.Snapshot{
		YesImpliedProbability:    0.64,
		RecentTradeFlowImbalance: -0.25,
		LiquidityDepth:           1_250,
		SecondsToResolution:      7_200.5,
		BlockLag:                 3,
	}
}

type fixedPredictor struct {
	value float64
	err   error
}

func (p fixedPredictor) Predict(_ []float64, _ int, output []float64) error {
	if p.err != nil {
		return p.err
	}
	output[0] = p.value
	return nil
}
