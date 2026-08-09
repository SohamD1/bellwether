package inference

import (
	"path/filepath"
	"testing"
)

var (
	benchmarkVector      [FeatureCount]float64
	benchmarkProbability float64
	benchmarkErr         error
)

func BenchmarkSnapshotVector(b *testing.B) {
	snapshot := validSnapshot()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkVector, benchmarkErr = SnapshotVector(snapshot)
	}
	if benchmarkErr != nil {
		b.Fatal(benchmarkErr)
	}
}

func BenchmarkModelPredict(b *testing.B) {
	model, err := Load(filepath.Join("testdata", "compatibility_model.txt"))
	if err != nil {
		b.Fatal(err)
	}
	snapshot := validSnapshot()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkProbability, benchmarkErr = model.Predict(snapshot)
	}
	if benchmarkErr != nil {
		b.Fatal(benchmarkErr)
	}
}
