package logs

import (
	"testing"

	xlog "github.com/xtls/xray-core/common/log"
)

func BenchmarkCollectorFiltered(b *testing.B) {
	c, err := NewCollector(DefaultRing, "")
	if err != nil {
		b.Fatal(err)
	}
	defer c.Close()
	c.SetLevel(LevelWarning)
	msg := &xlog.GeneralMessage{Severity: xlog.Severity_Info, Content: "proxy connection accepted tcp:example.com:443"}
	b.ReportAllocs()
	for b.Loop() {
		c.Handle(msg)
	}
}
