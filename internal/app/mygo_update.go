package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/egoist/mygo"
)

type mygoUpdateBackend struct{}

// NewMyGOUpdateBackend 返回 MyGO 的签名就地更新器适配器。
func NewMyGOUpdateBackend() UpdateBackend { return mygoUpdateBackend{} }

func (mygoUpdateBackend) Enabled() bool { return mygo.Updater.Enabled() }

func (mygoUpdateBackend) Check(ctx context.Context) (*UpdateRelease, error) {
	update, err := mygo.Updater.Check(ctx)
	if errors.Is(err, mygo.ErrUpdatesDisabled) {
		return nil, ErrUpdateUnavailable
	}
	if err != nil {
		return nil, err
	}
	if update == nil {
		return nil, nil
	}
	return &UpdateRelease{
		Version: update.Version,
		Notes:   update.Notes,
		Date:    update.Date,
		handle:  update,
	}, nil
}

func (mygoUpdateBackend) Install(ctx context.Context, release *UpdateRelease, progress func(downloaded, total int64)) error {
	if release == nil {
		return ErrUpdateUnavailable
	}
	update, ok := release.handle.(*mygo.Update)
	if !ok || update == nil {
		return fmt.Errorf("%w：缺少更新句柄", ErrUpdateUnavailable)
	}
	return update.Install(ctx, progress)
}

func (mygoUpdateBackend) Relaunch() { mygo.App.Relaunch() }
