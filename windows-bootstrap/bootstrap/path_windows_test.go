//go:build windows

package bootstrap

import "testing"

func TestPathIsWithinHandlesWindowsVolumes(t *testing.T) {
	tests := []struct {
		name      string
		parent    string
		candidate string
		want      bool
	}{
		{name: "child", parent: `C:\Users\Test\AppData`, candidate: `C:\Users\Test\AppData\wsl\backup.tar`, want: true},
		{name: "sibling", parent: `C:\Users\Test\AppData`, candidate: `C:\Users\Test\Documents\backup.tar`, want: false},
		{name: "different drive", parent: `C:\Users\Test\AppData`, candidate: `D:\Backups\backup.tar`, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := pathIsWithin(test.parent, test.candidate)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("pathIsWithin(%q, %q) = %t, want %t", test.parent, test.candidate, got, test.want)
			}
		})
	}
}
