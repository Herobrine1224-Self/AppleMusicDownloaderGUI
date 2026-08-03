//go:build windows

package main

import (
	"syscall"
	"unsafe"

	"github.com/lxn/win"
)

// trackLVHook keeps the track list behaving like a plain mark-based picker:
// clicking a row toggles its download checkmark, and the native cursor
// selection highlight is suppressed.

type trackLVHook struct {
	hwnd win.HWND
	orig uintptr
}

var (
	trackLVProcPtr uintptr
	trackLVHooks   []trackLVHook
	trackLVGui     *gui
)

func trackLVOrigFor(hwnd win.HWND) uintptr {
	for _, hook := range trackLVHooks {
		if hook.hwnd == hwnd {
			return hook.orig
		}
	}
	return 0
}

func trackLVWndProc(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	if msg == win.WM_LBUTTONDOWN {
		g := trackLVGui
		if g != nil && !g.busy && g.trackModel.RowCount() > 0 {
			var hti win.LVHITTESTINFO
			hti.Pt.X = win.GET_X_LPARAM(lParam)
			hti.Pt.Y = win.GET_Y_LPARAM(lParam)
			win.SendMessage(hwnd, win.LVM_HITTEST, 0, uintptr(unsafe.Pointer(&hti)))
			if hti.IItem >= 0 {
				// Clicking anywhere on the row toggles that song's
				// download checkmark.
				row := int(hti.IItem)
				checked := g.trackChecker.Checked(row)
				g.trackChecker.SetChecked(row, !checked)
				g.trackModel.PublishRowChanged(row)
				g.updateTrackSummary()
			}
		}
	}

	return win.CallWindowProc(trackLVOrigFor(hwnd), hwnd, msg, wParam, lParam)
}

// installTrackLVHooks hooks the native list-view windows of the track table
// after the window has been created, and wires up deselection. It must run on
// the GUI thread.
func installTrackLVHooks(g *gui) {
	trackLVGui = g
	if trackLVProcPtr == 0 {
		trackLVProcPtr = syscall.NewCallback(trackLVWndProc)
	}
	hwnd := g.trackTable.Handle()
	for child := win.GetWindow(hwnd, win.GW_CHILD); child != 0; child = win.GetWindow(child, win.GW_HWNDNEXT) {
		var name [64]uint16
		if _, err := win.GetClassName(child, &name[0], len(name)); err != nil {
			continue
		}
		if syscall.UTF16ToString(name[:]) != "SysListView32" {
			continue
		}
		orig := win.SetWindowLongPtr(child, win.GWLP_WNDPROC, trackLVProcPtr)
		if orig != 0 {
			trackLVHooks = append(trackLVHooks, trackLVHook{hwnd: child, orig: orig})
		}
	}
	g.trackTable.CurrentIndexChanged().Attach(func() {
		// The native list-view always highlights the row under the cursor.
		// CurrentIndexChanged is published while walk is still inside its
		// own SetCurrentIndex, so clearing directly there is swallowed by
		// the re-entry guard. Defer the clear to the message loop instead:
		// it still runs before WM_PAINT, so the highlight never shows and
		// only the checkmarks indicate download selection.
		if g.trackTable.CurrentIndex() > -1 {
			g.sync(func() {
				if g.trackTable.CurrentIndex() > -1 {
					_ = g.trackTable.SetCurrentIndex(-1)
				}
			})
		}
	})
}
