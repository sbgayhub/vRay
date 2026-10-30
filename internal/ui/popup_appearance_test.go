package ui

import "testing"

func TestParentAppearanceTracksPopupFocusAndMaterial(t *testing.T) {
	var active bool
	hold := &popupAppearance{apply: func(keep bool) { active = keep }}
	hold.setEnabled(true)
	if active {
		t.Fatal("unfocused popup forces parent appearance")
	}
	hold.setFocused(true)
	if !active {
		t.Fatal("parent appearance not retained for active popup")
	}
	hold.setFocused(false)
	if active {
		t.Fatal("parent remains forced active after popup loses focus")
	}
	hold.setFocused(true)
	if !active {
		t.Fatal("parent appearance not restored on popup focus")
	}
	hold.setEnabled(false)
	if active {
		t.Fatal("plain material retains forced appearance")
	}
	hold.setEnabled(true)
	hold.close()
	if active {
		t.Fatal("parent appearance not released when popup closes")
	}
	hold.setFocused(true)
	hold.setEnabled(true)
	if active {
		t.Fatal("stale event reactivated closed appearance hold")
	}
}
