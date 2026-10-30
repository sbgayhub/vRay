package app

import (
	"testing"

	"github.com/sbgayhub/vray/internal/core/logs"
)

func TestLogCollectorFollowsSavedSettings(t *testing.T) {
	svc, _ := newTestService(t)
	collector, err := logs.NewCollector(10, "")
	if err != nil {
		t.Fatal(err)
	}
	defer collector.Close()
	svc.AttachLogs(collector)
	if collector.Level() != logs.LevelWarning {
		t.Fatalf("startup threshold = %v", collector.Level())
	}
	for _, level := range []string{"debug", "info", "error", "none", "warning"} {
		settings := svc.State().Snapshot().Settings
		settings.LogLevel = level
		<-svc.SaveSettings(settings)
		if collector.Level().String() != level {
			t.Fatalf("saved %s, collector uses %s", level, collector.Level())
		}
	}
}
