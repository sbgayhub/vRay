package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sbgayhub/vray/internal/store"
)

func reopenedIPService(t *testing.T, previous *Service, server *httptest.Server) *Service {
	t.Helper()
	service, _ := NewFake(previous.paths.Root)
	service.secrets = previous.secrets
	if err := service.Load(); err != nil {
		t.Fatal(err)
	}
	service.noResolve = false
	useIPTestServer(service, server)
	t.Cleanup(service.CancelInspection)
	return service
}

func TestExitInfoPersistsAcrossRestartAndRefreshReplacesAllFields(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"ip":"8.8.8.8","company":{"name":"Old provider","type":"business","abuser_score":"0.01 (Low)"},"asn":{"asn":15169,"org":"Old network"},"location":{"country":"Old country","state":"Old region","city":"Old city"}}`))
		} else {
			// 新观测中缺失的字段也必须替换旧值。
			_, _ = w.Write([]byte(`{"ip":"8.8.4.4","company":"New provider","country":"New country"}`))
		}
	}))
	defer server.Close()
	svc, _ := newTestService(t)
	p := testProfile()
	p.Address = "127.0.0.1"
	if err := svc.AddTestProfile(p, "secret-node-password"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	useIPTestServer(svc, server)
	awaitOperation(t, svc.InspectNode(p.ID))
	d, _ := svc.store.Load()
	saved := d.NodeDetails[p.ID]
	if saved.IP != "8.8.8.8" || saved.City != "Old city" || saved.ASN != "AS15169 · Old network" || saved.Score != "0.01 (Low)" || saved.CheckedAt.IsZero() {
		t.Fatalf("incomplete durable record: %+v", saved)
	}
	raw, _ := json.Marshal(d)
	if strings.Contains(string(raw), "secret-node-password") || strings.Contains(string(raw), `"Loading"`) || strings.Contains(string(raw), `"request"`) {
		t.Fatal("credentials or transient request state were persisted")
	}
	restarted := reopenedIPService(t, svc, server)
	awaitOperation(t, restarted.InspectSelectedNode(p.ID))
	info := restarted.State().Snapshot().NodeDetails[p.ID]
	if requests.Load() != 1 || info.stored() != saved || info.Loading || info.Error != "" {
		t.Fatalf("restart did not reuse all metadata without a request: requests=%d info=%+v", requests.Load(), info)
	}
	awaitOperation(t, restarted.InspectNode(p.ID))
	d, _ = restarted.store.Load()
	updated := d.NodeDetails[p.ID]
	if requests.Load() != 2 || updated.IP != "8.8.4.4" || updated.Country != "New country" || updated.Provider != "New provider" || updated.City != "" || updated.ASN != "" || updated.Score != "" {
		t.Fatalf("refresh did not fully replace persisted metadata: %+v", updated)
	}
	reopened := reopenedIPService(t, restarted, server)
	awaitOperation(t, reopened.InspectSelectedNode(p.ID))
	if requests.Load() != 2 || reopened.State().Snapshot().NodeDetails[p.ID].stored() != updated {
		t.Fatal("refreshed metadata was not preserved for the next restart")
	}
}

type failIPSaveStore struct{ store.Store }

func (f failIPSaveStore) Save(store.Data) error { return errors.New("write failed") }

func TestFailedRefreshOrSaveKeepsPreviousDurableExit(t *testing.T) {
	var requests atomic.Int32
	var unavailable atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if unavailable.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"ip":"8.8.8.8","country":"Saved country"}`))
	}))
	defer server.Close()
	svc, _ := newTestService(t)
	p := testProfile()
	p.Address = "127.0.0.1"
	if err := svc.AddTestProfile(p, "secret"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	useIPTestServer(svc, server)
	awaitOperation(t, svc.InspectNode(p.ID))
	d, _ := svc.store.Load()
	saved := d.NodeDetails[p.ID]
	for _, failSave := range []bool{false, true} {
		unavailable.Store(!failSave)
		if failSave {
			svc.store = failIPSaveStore{svc.store}
		}
		awaitOperation(t, svc.InspectNode(p.ID))
		info := svc.State().Snapshot().NodeDetails[p.ID]
		d, _ = svc.store.Load()
		if info.stored() != saved || info.Error == "" || info.Loading || d.NodeDetails[p.ID] != saved {
			t.Fatalf("failed refresh replaced the previous record: info=%+v saved=%+v", info, d.NodeDetails[p.ID])
		}
		if failSave && !strings.Contains(info.Error, "保存失败") {
			t.Fatal("storage failure was not reported")
		}
	}
	restarted := reopenedIPService(t, svc, server)
	awaitOperation(t, restarted.InspectSelectedNode(p.ID))
	if requests.Load() != 3 || restarted.State().Snapshot().NodeDetails[p.ID].stored() != saved {
		t.Fatal("failed refresh caused a request or data loss on restart")
	}
}

func TestLateExitQueryCannotRestoreDeletedNode(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte(`{"ip":"8.8.8.8"}`))
	}))
	defer server.Close()
	svc, _ := newTestService(t)
	p := testProfile()
	if err := svc.AddTestProfile(p, "secret"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	useIPTestServer(svc, server)
	done := svc.InspectNode(p.ID)
	select {
	case <-started:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("lookup did not start")
	}
	if err := svc.store.DeleteProfiles([]string{p.ID}); err != nil {
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
	if _, exists := d.NodeDetails[p.ID]; exists || len(svc.State().Snapshot().NodeDetails) != 0 {
		t.Fatal("late result resurrected deleted node metadata")
	}
}
