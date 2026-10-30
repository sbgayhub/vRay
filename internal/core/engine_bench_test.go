package core

import (
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkEngineStartStopGeo(b *testing.B) {
	dir, err := filepath.Abs(filepath.Join("..", "..", "data"))
	if err != nil {
		b.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "geosite.dat")); err != nil {
		b.Skip("public geo data unavailable")
	}
	b.Setenv(assetLocationEnv, dir)
	b.ReportAllocs()
	cfg := []byte(geoConfig)
	for b.Loop() {
		e := New()
		if err := e.Start(cfg); err != nil {
			b.Fatal(err)
		}
		if err := e.Stop(); err != nil {
			b.Fatal(err)
		}
	}
}
