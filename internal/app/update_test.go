package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sbgayhub/vray/internal/paths"
	"github.com/sbgayhub/vray/internal/store"
)

func newUnconsentedService(t *testing.T) (*Service, *FakeEngine) {
	t.Helper()
	p := paths.Paths{Root: t.TempDir()}
	engine := &FakeEngine{}
	svc := New(
		NewState(Snapshot{Phase: PhaseIdle, Mode: store.DefaultSettings().Mode}),
		store.NewFile(p.DB()),
		NewMemSecrets(),
		engine,
		p,
	)
	svc.noResolve = true
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return svc, engine
}

type fakeUpdateBackend struct {
	check      func(context.Context) (*UpdateRelease, error)
	install    func(context.Context, *UpdateRelease, func(int64, int64)) error
	checkCalls atomic.Int32
	installLog atomic.Int32
	relaunches atomic.Int32
	enabled    bool
}

func (f *fakeUpdateBackend) Enabled() bool { return f.enabled }

func (f *fakeUpdateBackend) Check(ctx context.Context) (*UpdateRelease, error) {
	f.checkCalls.Add(1)
	if f.check != nil {
		return f.check(ctx)
	}
	return nil, nil
}

func (f *fakeUpdateBackend) Install(ctx context.Context, release *UpdateRelease, progress func(int64, int64)) error {
	f.installLog.Add(1)
	if f.install != nil {
		return f.install(ctx, release, progress)
	}
	return nil
}

func (f *fakeUpdateBackend) Relaunch() { f.relaunches.Add(1) }

func waitForCondition(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestFirstRunAgreementPersistsAndStartsNetworkWorkOnce(t *testing.T) {
	svc, engine := newUnconsentedService(t)
	if !svc.State().Snapshot().ConsentRequired {
		t.Fatal("fresh install did not require the legal agreement")
	}

	var starts atomic.Int32
	svc.SetPostConsentStart(func() { starts.Add(1) })
	svc.StartPostConsent()
	if starts.Load() != 0 {
		t.Fatal("post-consent work started before acceptance")
	}

	select {
	case <-svc.AcceptTerms():
	case <-time.After(3 * time.Second):
		t.Fatal("accepting the terms did not finish")
	}
	if svc.State().Snapshot().ConsentRequired {
		t.Fatal("agreement remained required after a successful save")
	}
	if starts.Load() != 1 {
		t.Fatalf("post-consent work ran %d times, want once", starts.Load())
	}
	svc.StartPostConsent()
	if starts.Load() != 1 {
		t.Fatalf("post-consent work repeated: %d", starts.Load())
	}

	d, err := svc.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if d.Settings.TermsAcceptedVersion != store.LegalVersion || d.Settings.TermsAcceptedAt.IsZero() {
		t.Fatalf("agreement was not persisted: %+v", d.Settings)
	}

	reopened := New(
		NewState(Snapshot{Phase: PhaseIdle}),
		store.NewFile(svc.paths.DB()),
		NewMemSecrets(),
		engine,
		svc.paths,
	)
	reopened.noResolve = true
	if err := reopened.Load(); err != nil {
		t.Fatal(err)
	}
	if reopened.State().Snapshot().ConsentRequired {
		t.Fatal("reopening the app asked for the same agreement again")
	}
}

func TestUpdateChecksWaitForAgreementAndCoalesce(t *testing.T) {
	svc, _ := newUnconsentedService(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	backend := &fakeUpdateBackend{
		enabled: true,
		check: func(context.Context) (*UpdateRelease, error) {
			entered <- struct{}{}
			<-release
			return &UpdateRelease{Version: "0.3.0", Notes: "Release notes", Date: time.Now()}, nil
		},
	}
	svc.AttachUpdateBackend(backend)

	svc.CheckForUpdates()
	time.Sleep(30 * time.Millisecond)
	if backend.checkCalls.Load() != 0 {
		t.Fatal("update check contacted the network before agreement")
	}

	select {
	case <-svc.AcceptTerms():
	case <-time.After(3 * time.Second):
		t.Fatal("accepting terms did not finish")
	}
	svc.CheckForUpdates()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the first update check did not start")
	}
	svc.CheckForUpdates()
	close(release)

	waitForCondition(t, "the update result", func() bool {
		return svc.State().Snapshot().Update.AvailableVersion == "0.3.0"
	})
	if calls := backend.checkCalls.Load(); calls != 1 {
		t.Fatalf("concurrent checks reached the backend %d times, want once", calls)
	}
	update := svc.State().Snapshot().Update
	if update.Notes != "Release notes" || update.Checking || update.UpToDate || update.Error != "" {
		t.Fatalf("published update state = %+v", update)
	}
}

func TestAutomaticUpdateScheduleRunsOncePerDayAndHonorsTheToggle(t *testing.T) {
	svc, _ := newTestService(t)
	backend := &fakeUpdateBackend{enabled: true}
	svc.AttachUpdateBackend(backend)
	svc.State().Update(func(sn *Snapshot) { sn.Settings.AutoUpdateChecks = true })

	first := time.Date(2026, 10, 30, 4, 1, 0, 0, time.Local)
	svc.runScheduledUpdate(first)
	waitForCondition(t, "the first daily update check", func() bool {
		return backend.checkCalls.Load() == 1 && !svc.State().Snapshot().Update.Checking
	})
	svc.runScheduledUpdate(first.Add(30 * time.Minute))
	time.Sleep(30 * time.Millisecond)
	if calls := backend.checkCalls.Load(); calls != 1 {
		t.Fatalf("same-day schedule checked %d times, want once", calls)
	}

	svc.runScheduledUpdate(first.Add(24 * time.Hour))
	waitForCondition(t, "the next daily update check", func() bool { return backend.checkCalls.Load() == 2 })

	svc.State().Update(func(sn *Snapshot) { sn.Settings.AutoUpdateChecks = false })
	svc.runScheduledUpdate(first.Add(48 * time.Hour))
	time.Sleep(30 * time.Millisecond)
	if calls := backend.checkCalls.Load(); calls != 2 {
		t.Fatalf("disabled automatic checks still reached the backend (%d calls)", calls)
	}
}

func TestUpdateInstallPublishesProgressAndCanRelaunch(t *testing.T) {
	svc, _ := newTestService(t)
	backend := &fakeUpdateBackend{
		enabled: true,
		check: func(context.Context) (*UpdateRelease, error) {
			return &UpdateRelease{Version: "0.3.0"}, nil
		},
		install: func(_ context.Context, release *UpdateRelease, progress func(int64, int64)) error {
			if release == nil || release.Version != "0.3.0" {
				return errors.New("wrong release")
			}
			progress(25, 100)
			time.Sleep(110 * time.Millisecond)
			progress(100, 100)
			return nil
		},
	}
	svc.AttachUpdateBackend(backend)
	svc.CheckForUpdates()
	waitForCondition(t, "the available update", func() bool {
		return !svc.State().Snapshot().Update.Checking && svc.State().Snapshot().Update.AvailableVersion == "0.3.0"
	})

	svc.InstallUpdate()
	waitForCondition(t, "the installed update", func() bool {
		return svc.State().Snapshot().Update.Installed
	})
	update := svc.State().Snapshot().Update
	if update.Installing || update.Downloaded != 100 || update.Total != 100 || update.Error != "" {
		t.Fatalf("installed update state = %+v", update)
	}
	if backend.installLog.Load() != 1 {
		t.Fatalf("install backend called %d times", backend.installLog.Load())
	}

	svc.RelaunchForUpdate()
	if backend.relaunches.Load() != 1 {
		t.Fatal("restart action did not relaunch the staged update")
	}
}

func TestUnavailableUpdaterIsVisibleAndDoesNotStartARequest(t *testing.T) {
	svc, _ := newTestService(t)
	backend := &fakeUpdateBackend{enabled: false}
	svc.AttachUpdateBackend(backend)
	svc.CheckForUpdates()

	update := svc.State().Snapshot().Update
	if update.Enabled || update.Error == "" {
		t.Fatalf("unavailable updater state = %+v", update)
	}
	if backend.checkCalls.Load() != 0 {
		t.Fatal("unavailable updater was asked to check")
	}
}
