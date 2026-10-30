package app

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sbgayhub/vray/internal/store"
)

func downloadService(t *testing.T, handler http.Handler) (*Service, string, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	svc, _ := newTestService(t)
	profile := testProfile()
	if err := svc.AddTestProfile(profile, "private-test-secret"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	useIPTestServer(svc, server)
	svc.downloadEndpoint = server.URL + "/download"
	svc.downloadLimits = downloadLimits{MaxBytes: 64 << 10, Duration: time.Second, Timeout: 3 * time.Second}
	t.Cleanup(svc.CancelInspection)
	return svc, profile.ID, server
}

func seedDownload(t *testing.T, svc *Service, id string) store.DownloadSpeed {
	t.Helper()
	old := store.DownloadSpeed{BytesPerSecond: 123456, Bytes: 65536, Duration: time.Second, CheckedAt: time.Now().Add(-24 * time.Hour).UTC(), Endpoint: "https://example.com/previous"}
	d, err := svc.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	d.NodeSpeeds = map[string]store.DownloadSpeed{id: old}
	if err := svc.store.Save(d); err != nil {
		t.Fatal(err)
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	return old
}

func writeDownloadFixture(w http.ResponseWriter, size int) {
	w.Header().Set("Content-Type", "application/octet-stream")
	first := min(size/2, 16<<10)
	_, _ = w.Write(bytes.Repeat([]byte{0xab}, first))
	w.(http.Flusher).Flush()
	// 可测量的响应体写入间隔，不受本地回环和时钟精度影响。
	time.Sleep(20 * time.Millisecond)
	_, _ = w.Write(bytes.Repeat([]byte{0xab}, size-first))
}

func TestDownloadPersistsAndOnlyChangesAfterManualRetest(t *testing.T) {
	var requests atomic.Int32
	svc, id, _ := downloadService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("download request must be an uncompressed GET")
		}
		size, _ := strconv.Atoi(r.URL.Query().Get("bytes"))
		writeDownloadFixture(w, size)
	}))
	if requests.Load() != 0 {
		t.Fatal("service automatically started a download")
	}
	awaitOperation(t, svc.TestDownload(id))
	result := svc.State().Snapshot().NodeSpeeds[id]
	d, _ := svc.store.Load()
	if result.Running || result.Error != "" || result.Bytes != 64<<10 || result.BytesPerSecond <= 0 || result.Duration <= 0 || result.CheckedAt.IsZero() || d.NodeSpeeds[id] != result.DownloadSpeed {
		t.Fatalf("invalid download result: %+v", result)
	}
	if d.Usage[id].Download != result.Bytes {
		t.Fatal("download traffic was not counted and persisted once")
	}
	restarted, _ := NewFake(svc.paths.Root)
	if err := restarted.Load(); err != nil {
		t.Fatal(err)
	}
	if restarted.State().Snapshot().NodeSpeeds[id].DownloadSpeed != result.DownloadSpeed || requests.Load() != 1 {
		t.Fatal("restart lost the speed or repeated the test")
	}
	svc.downloadLimits.MaxBytes = 128 << 10
	awaitOperation(t, svc.TestDownload(id))
	d, _ = svc.store.Load()
	if requests.Load() != 2 || d.NodeSpeeds[id].Bytes != 128<<10 || !d.NodeSpeeds[id].CheckedAt.After(result.CheckedAt) || d.Usage[id].Download != 192<<10 {
		t.Fatalf("retest failed to replace record or count traffic: speed=%+v usage=%+v", d.NodeSpeeds[id], d.Usage[id])
	}
	if err := restarted.Load(); err != nil {
		t.Fatal(err)
	}
	if restarted.State().Snapshot().NodeSpeeds[id].DownloadSpeed != d.NodeSpeeds[id] {
		t.Fatal("restarted service did not read replaced result")
	}
}

func TestDownloadFailureAndSaveFailureKeepOldResult(t *testing.T) {
	var failed atomic.Bool
	failed.Store(true)
	svc, id, _ := downloadService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failed.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeDownloadFixture(w, 64<<10)
	}))
	old := seedDownload(t, svc, id)
	for _, failSave := range []bool{false, true} {
		if failSave {
			failed.Store(false)
			svc.store = failIPSaveStore{svc.store}
		}
		awaitOperation(t, svc.TestDownload(id))
		result := svc.State().Snapshot().NodeSpeeds[id]
		d, _ := svc.store.Load()
		if result.Running || result.Error == "" || result.DownloadSpeed != old || d.NodeSpeeds[id] != old {
			t.Fatalf("failed test overwrote record: %+v", result)
		}
		if failSave && !strings.Contains(result.Error, "保存失败") {
			t.Fatal("save failure was not reported")
		}
	}
}

func TestDownloadByteCapAndRateUnits(t *testing.T) {
	svc, id, _ := downloadService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeDownloadFixture(w, 128<<10)
	}))
	awaitOperation(t, svc.TestDownload(id))
	result := svc.State().Snapshot().NodeSpeeds[id]
	if result.Error != "" || result.Bytes != 64<<10 {
		t.Fatalf("byte cap not enforced: %+v", result)
	}
	if downloadRate(1<<20, 2*time.Second) != 512<<10 {
		t.Fatal("speed is not measured in bytes per second")
	}
}

func TestDownloadDurationLimitReturnsMeasuredPartialResult(t *testing.T) {
	svc, id, _ := downloadService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(bytes.Repeat([]byte{0x33}, 16<<10))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	svc.downloadLimits.Duration = 60 * time.Millisecond
	awaitOperation(t, svc.TestDownload(id))
	result := svc.State().Snapshot().NodeSpeeds[id]
	if result.Error != "" || result.Bytes != 16<<10 || result.Duration < 40*time.Millisecond || result.Duration > time.Second {
		t.Fatalf("timed partial test failed: %+v", result)
	}
}

func TestDownloadCancellationReleasesConnectionAndKeepsRecord(t *testing.T) {
	released := make(chan struct{})
	svc, id, _ := downloadService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(bytes.Repeat([]byte{0x11}, 16<<10))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(released)
	}))
	old := seedDownload(t, svc, id)
	done := svc.TestDownload(id)
	deadline := time.Now().Add(time.Second)
	for svc.State().Snapshot().NodeSpeeds[id].CurrentBytes < 16<<10 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	svc.CancelDownload()
	awaitOperation(t, done)
	result := svc.State().Snapshot().NodeSpeeds[id]
	if result.Running || result.DownloadSpeed != old || !strings.Contains(result.Error, "取消") {
		t.Fatalf("cancel failed: %+v", result)
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("cancel did not close the download connection")
	}
	d, _ := svc.store.Load()
	if d.NodeSpeeds[id] != old || d.Usage[id].Download != 16<<10 {
		t.Fatal("cancel lost saved speed or actual downloaded traffic")
	}
}

func TestDownloadRejectsErrorPagesAndTruncatedBodies(t *testing.T) {
	for _, kind := range []string{"html", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			svc, id, _ := downloadService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "html" {
					w.Header().Set("Content-Type", "text/html")
					_, _ = io.WriteString(w, "<html>not a file</html>")
					return
				}
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Content-Length", "65536")
				_, _ = w.Write([]byte{1, 2, 3})
			}))
			awaitOperation(t, svc.TestDownload(id))
			if result := svc.State().Snapshot().NodeSpeeds[id]; result.Error == "" || !result.CheckedAt.IsZero() {
				t.Fatalf("bad response accepted: %+v", result)
			}
		})
	}
}

func TestDeletedNodeDoesNotReceiveLateDownloadResult(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	svc, id, _ := downloadService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(bytes.Repeat([]byte{0x44}, 64<<10))
	}))
	done := svc.TestDownload(id)
	<-started
	if err := svc.store.DeleteProfiles([]string{id}); err != nil {
		close(release)
		t.Fatal(err)
	}
	if err := svc.Load(); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	awaitOperation(t, done)
	d, _ := svc.store.Load()
	if len(d.NodeSpeeds) != 0 || len(svc.State().Snapshot().NodeSpeeds) != 0 {
		t.Fatal("late download resurrected deleted node metadata")
	}
}

// 验证快照不会共享用于进度更新的可变 map。
func TestDownloadSnapshotIsIndependent(t *testing.T) {
	state := NewState(Snapshot{NodeSpeeds: map[string]DownloadTest{"node": {Running: true}}})
	snapshot := state.Snapshot()
	snapshot.NodeSpeeds["node"] = DownloadTest{}
	if !state.Snapshot().NodeSpeeds["node"].Running {
		t.Fatal("snapshot shares its download state map")
	}
}
