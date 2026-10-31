package domain

import "testing"

func TestDirectoryModeUsesObservableWindowsPermissions(t *testing.T) {
	for _, tc := range []struct {
		mode     uint32
		platform string
		want     uint32
	}{
		{0755, "windows", 0777}, {0700, "windows", 0777}, {0777, "windows", 0777},
		{0555, "windows", 0555}, {0444, "windows", 0555},
		{0755, "darwin", 0755}, {0700, "linux", 0700},
	} {
		if got := directoryModeForOS(tc.mode, tc.platform); got != tc.want {
			t.Fatalf("%s %04o: got %04o want %04o", tc.platform, tc.mode, got, tc.want)
		}
	}
}

func TestDirectoryDescriptorEqualityWindows(t *testing.T) {
	dir := func(mode uint32) Descriptor { return Descriptor{Type: "directory", Mode: mode} }
	if !sameDescriptorForOS(dir(0755), dir(0777), "windows") {
		t.Fatal("equivalent writable directory modes rejected")
	}
	if sameDescriptorForOS(dir(0555), dir(0777), "windows") {
		t.Fatal("read-only change accepted")
	}
	if sameDescriptorForOS(dir(0755), dir(0777), "darwin") {
		t.Fatal("POSIX permission change accepted")
	}
	if sameDescriptorForOS(Descriptor{Type: "file", Mode: 0755}, Descriptor{Type: "file", Mode: 0777}, "windows") {
		t.Fatal("file comparison weakened")
	}
	if sameDescriptorForOS(dir(0755), Descriptor{Type: "file", Mode: 0777}, "windows") {
		t.Fatal("directory replacement accepted")
	}
}
