package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sbgayhub/vray/internal/store"
)

const managedLink = "vless://11111111-2222-3333-4444-555555555555@jp.example.com:443?security=tls#Tokyo"

func managedSource(t *testing.T, svc *Service, rawURL string) store.Source {
	t.Helper()
	src := store.Source{ID: "source-test", Name: "Tokyo subscription", URLRef: "source/test/url", Enabled: true}
	if err := svc.secrets.Set(src.URLRef, []byte(rawURL)); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.PutSource(src); err != nil {
		t.Fatal(err)
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	return src
}

type sourceRoundTrip func(*http.Request) (*http.Response, error)

func (fn sourceRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestAutomaticSourceUpdateHonorsIntervalAndStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc, _ := newTestService(t)
		src := managedSource(t, svc, "https://subscription.example.com/list")
		src.LastFetchedAt, src.IntervalMinutes = time.Now(), 15
		if err := svc.store.PutSource(src); err != nil {
			t.Fatal(err)
		}
		if err := svc.Load(); err != nil {
			t.Fatal(err)
		}
		var requests atomic.Int32
		svc.httpClient = &http.Client{Transport: sourceRoundTrip(func(r *http.Request) (*http.Response, error) {
			requests.Add(1)
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(managedLink)), Header: make(http.Header)}, nil
		})}
		ctx, cancel := context.WithCancel(t.Context())
		done := svc.RunSourceScheduler(ctx)
		time.Sleep(14 * time.Minute)
		synctest.Wait()
		if requests.Load() != 0 {
			t.Fatal("subscription refreshed before its interval")
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if requests.Load() != 1 || len(svc.State().Snapshot().Profiles) != 1 {
			t.Fatal("scheduled refresh did not complete")
		}
		cancel()
		<-done
	})
}

func awaitOperation(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("operation did not finish")
	}
}

func TestManagedSourceEditRefreshAndDelete(t *testing.T) {
	for _, removeNodes := range []bool{false, true} {
		name := "keep nodes"
		if removeNodes {
			name = "remove nodes"
		}
		t.Run(name, func(t *testing.T) {
			var body atomic.Value
			body.Store(managedLink)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Subscription-Userinfo", "upload=1024; download=2048; total=8192; expire=1900000000")
				_, _ = w.Write([]byte(body.Load().(string)))
			}))
			defer server.Close()
			svc, _ := newTestService(t)
			src := managedSource(t, svc, server.URL+"/secret-token")
			awaitOperation(t, svc.RefreshSource(src.ID))
			snap := svc.State().Snapshot()
			if len(snap.Profiles) != 1 || snap.Sources[0].Total != 8192 {
				t.Fatalf("refresh = %+v", snap.Sources)
			}
			id := snap.Profiles[0].ID
			p, err := svc.store.GetProfile(id)
			if err != nil {
				t.Fatal(err)
			}
			p.Favorite, p.Name = true, "My alias"
			if err := svc.store.PutProfile(p); err != nil {
				t.Fatal(err)
			}
			cachedData, err := svc.store.Load()
			if err != nil {
				t.Fatal(err)
			}
			cached := store.NodeIPInfo{IP: "8.8.8.8", Country: "Saved country", CheckedAt: time.Now().UTC()}
			cachedData.NodeDetails = map[string]store.NodeIPInfo{id: cached}
			cachedSpeed := store.DownloadSpeed{BytesPerSecond: 2 << 20, Bytes: 32 << 20, Duration: 16 * time.Second, CheckedAt: time.Now().UTC()}
			cachedData.NodeSpeeds = map[string]store.DownloadSpeed{id: cachedSpeed}
			if err := svc.store.Save(cachedData); err != nil {
				t.Fatal(err)
			}
			awaitOperation(t, svc.RefreshSource(src.ID))
			snap = svc.State().Snapshot()
			if snap.Profiles[0].ID != id || snap.Profiles[0].Name != "My alias" || !snap.Profiles[0].Favorite {
				t.Fatalf("refresh lost node identity: %+v", snap.Profiles)
			}
			if snap.NodeDetails[id].stored() != cached {
				t.Fatal("subscription refresh discarded saved exit metadata")
			}
			if snap.NodeSpeeds[id].DownloadSpeed != cachedSpeed {
				t.Fatal("subscription refresh discarded download speed")
			}
			body.Store("this is not a subscription")
			awaitOperation(t, svc.RefreshSource(src.ID))
			if got := svc.State().Snapshot(); len(got.Profiles) != 1 || got.Profiles[0].ID != id || got.Sources[0].LastError == "" {
				t.Fatalf("failed refresh destroyed the working list: %+v", got.Sources)
			}
			edit := SourceEdit{ID: src.ID, Name: "Renamed", URL: server.URL + "/new-secret", UserAgent: "My client", IntervalMinutes: 30, Enabled: true}
			awaitOperation(t, svc.SaveSource(edit))
			d, err := svc.store.Load()
			if err != nil {
				t.Fatal(err)
			}
			urlKey := d.Sources[0].URLRef
			url, err := svc.secrets.Get(urlKey)
			if err != nil || string(url) != edit.URL || d.Sources[0].Name != "Renamed" || d.Sources[0].IntervalMinutes != 30 {
				t.Fatalf("source edit did not persist: %+v %v", d.Sources, err)
			}
			encoded, _ := json.Marshal(d)
			if strings.Contains(string(encoded), "new-secret") {
				t.Fatal("subscription URL leaked into ordinary storage")
			}
			if strings.Contains(string(encoded), "11111111-2222-3333-4444-555555555555") {
				t.Fatal("node credential leaked into raw outbound storage")
			}
			awaitOperation(t, svc.DeleteSource(src.ID, removeNodes))
			d, err = svc.store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if len(d.Sources) != 0 {
				t.Fatal("source was not removed")
			}
			if _, err := svc.secrets.Get(urlKey); err == nil {
				t.Fatal("deleted subscription URL was retained")
			}
			if removeNodes && len(d.Profiles) != 0 {
				t.Fatal("associated nodes were not deleted")
			}
			if !removeNodes && (len(d.Profiles) != 1 || d.Profiles[0].SourceID != "") {
				t.Fatal("kept nodes were not detached")
			}
			if removeNodes && len(d.NodeDetails) != 0 {
				t.Fatal("deleted subscription nodes retained exit metadata")
			}
			if !removeNodes && d.NodeDetails[id] != cached {
				t.Fatal("detaching a kept node discarded exit metadata")
			}
			if removeNodes && len(d.NodeSpeeds) != 0 {
				t.Fatal("deleted subscription nodes retained speed results")
			}
			if !removeNodes && d.NodeSpeeds[id] != cachedSpeed {
				t.Fatal("detaching a node discarded speed results")
			}
		})
	}
}

func TestRefreshCannotResurrectDeletedSource(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte(managedLink))
	}))
	defer server.Close()
	svc, _ := newTestService(t)
	src := managedSource(t, svc, server.URL)
	done := svc.RefreshSource(src.ID)
	<-entered
	awaitOperation(t, svc.DeleteSource(src.ID, false))
	close(release)
	awaitOperation(t, done)
	snap := svc.State().Snapshot()
	if len(snap.Sources) != 0 || len(snap.Profiles) != 0 {
		t.Fatal("stale response resurrected deleted data")
	}
}

func TestRefreshKeepsAccountsOnSameEndpointSeparate(t *testing.T) {
	second := strings.Replace(managedLink, "11111111-2222-3333-4444-555555555555", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", 1)
	var reversed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := managedLink + "\n" + second
		if reversed.Load() {
			body = second + "\n" + managedLink
		}
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	svc, _ := newTestService(t)
	src := managedSource(t, svc, server.URL)
	awaitOperation(t, svc.RefreshSource(src.ID))
	before := svc.State().Snapshot().Profiles
	if len(before) != 2 {
		t.Fatalf("profiles=%d", len(before))
	}
	reversed.Store(true)
	awaitOperation(t, svc.RefreshSource(src.ID))
	after := svc.State().Snapshot().Profiles
	if len(after) != 2 || before[0].ID != after[1].ID || before[1].ID != after[0].ID {
		t.Fatal("same-endpoint accounts lost their identities on reorder")
	}
}

func TestRefreshCannotOverwriteEditedURL(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte(managedLink))
	}))
	defer server.Close()
	svc, _ := newTestService(t)
	src := managedSource(t, svc, server.URL)
	done := svc.RefreshSource(src.ID)
	<-entered
	edit := SourceEdit{ID: src.ID, Name: "New source", URL: "https://new.example.com/sub", Enabled: true}
	awaitOperation(t, svc.SaveSource(edit))
	close(release)
	awaitOperation(t, done)
	snap := svc.State().Snapshot()
	if len(snap.Profiles) != 0 || snap.Sources[0].Name != "New source" {
		t.Fatal("stale fetch overwrote the edited source")
	}
}
