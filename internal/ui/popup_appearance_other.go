//go:build !windows

package ui

import "github.com/egoist/mygo"

func parentAppearanceSetter(parent *mygo.Window) func(bool) {
	return func(keep bool) {
		if !parent.IsDestroyed() {
			parent.SetActiveAppearance(keep)
		}
	}
}
