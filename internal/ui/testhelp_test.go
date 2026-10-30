package ui

import (
	"time"

	"github.com/sbgayhub/vray/internal/app"
	"github.com/sbgayhub/vray/internal/store"
)

// profileForTest 返回 configgen 可接受的最小节点。
func profileForTest() store.Profile {
	return store.Profile{
		ID: "p1", Name: "tokyo", Protocol: store.ProtoVLESS,
		Address: "jp.example.com", Port: 443,
	}
}

// waitPhaseIn 轮询服务直到其进入 phase；供像点击一样驱动服务的测试使用。
func waitPhaseIn(svc *app.Service, phase app.Phase) bool {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if svc.State().Snapshot().Phase == phase {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}
