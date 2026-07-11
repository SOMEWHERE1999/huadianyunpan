//go:build windows && cgo

package main

import (
	"reflect"
	"testing"
)

func TestMountOptions(t *testing.T) {
	tests := []struct {
		name     string
		readOnly bool
		want     []string
	}{
		{name: "read-only", readOnly: true, want: []string{"-o", "ro"}},
		{name: "mkdir-only uses writable mount", readOnly: false, want: nil},
		{name: "default uses writable mount", readOnly: false, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mountOptions(tt.readOnly); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("mountOptions(%v)=%q, want %q", tt.readOnly, got, tt.want)
			}
		})
	}
}
