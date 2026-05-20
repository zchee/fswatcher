package fswatcher_test

import (
	"fmt"
	"reflect"
	"runtime"
	"testing"

	"github.com/fswatcher/fswatcher"
)

var (
	_ func() (*fswatcher.Watcher, error) = fswatcher.NewWatcher

	_ func(*fswatcher.Watcher, string, fswatcher.Op) error = (*fswatcher.Watcher).Add
	_ func(*fswatcher.Watcher, string, fswatcher.Op) error = (*fswatcher.Watcher).AddRecursive
	_ func(*fswatcher.Watcher, string) error               = (*fswatcher.Watcher).Remove
	_ func(*fswatcher.Watcher) error                       = (*fswatcher.Watcher).Close

	_ func(fswatcher.Event) string          = fswatcher.Event.String
	_ func(fswatcher.Op, fswatcher.Op) bool = fswatcher.Op.Has
	_ func(fswatcher.Op) string             = fswatcher.Op.String
	_ fmt.Stringer                          = fswatcher.Event{}
	_ fmt.Stringer                          = fswatcher.Op(0)
)

func TestPublicWatcherAPIContract(t *testing.T) {
	watcherType := reflect.TypeOf((*fswatcher.Watcher)(nil)).Elem()

	wantFields := []struct {
		name string
		typ  reflect.Type
	}{
		{"Events", watcherEventsType()},
		{"Errors", watcherErrorsType()},
	}
	assertExportedFields(t, watcherType, wantFields)

	wantMethods := []struct {
		name string
		typ  reflect.Type
	}{
		{"Add", reflect.TypeOf((*fswatcher.Watcher).Add)},
		{"AddRecursive", reflect.TypeOf((*fswatcher.Watcher).AddRecursive)},
		{"Close", reflect.TypeOf((*fswatcher.Watcher).Close)},
		{"Remove", reflect.TypeOf((*fswatcher.Watcher).Remove)},
	}
	assertExportedMethods(t, reflect.TypeOf((*fswatcher.Watcher)(nil)), wantMethods)
}

func TestPublicEventAPIContract(t *testing.T) {
	eventType := reflect.TypeOf(fswatcher.Event{})

	wantFields := []struct {
		name string
		typ  reflect.Type
	}{
		{"Name", reflect.TypeOf("")},
		{"Op", reflect.TypeOf(fswatcher.Op(0))},
	}
	assertExportedFields(t, eventType, wantFields)

	wantMethods := []struct {
		name string
		typ  reflect.Type
	}{
		{"String", reflect.TypeOf(fswatcher.Event.String)},
	}
	assertExportedMethods(t, eventType, wantMethods)
}

func TestPublicOpAPIContract(t *testing.T) {
	opType := reflect.TypeOf(fswatcher.Op(0))
	if got, want := opType.Kind(), reflect.Uint32; got != want {
		t.Fatalf("Op kind = %s, want %s", got, want)
	}

	wantConstants := []struct {
		name string
		got  fswatcher.Op
		want fswatcher.Op
	}{
		{"Create", fswatcher.Create, 1 << 0},
		{"Write", fswatcher.Write, 1 << 1},
		{"Remove", fswatcher.Remove, 1 << 2},
		{"Rename", fswatcher.Rename, 1 << 3},
		{"Chmod", fswatcher.Chmod, 1 << 4},
		{"All", fswatcher.All, fswatcher.Create | fswatcher.Write | fswatcher.Remove | fswatcher.Rename | fswatcher.Chmod},
	}
	for _, tt := range wantConstants {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("%s = %d, want %d", tt.name, tt.got, tt.want)
			}
		})
	}

	wantMethods := []struct {
		name string
		typ  reflect.Type
	}{
		{"Has", reflect.TypeOf(fswatcher.Op.Has)},
		{"String", reflect.TypeOf(fswatcher.Op.String)},
	}
	assertExportedMethods(t, opType, wantMethods)
}

func watcherEventsType() reflect.Type {
	if supportedWatcherBackend(runtime.GOOS) {
		return reflect.TypeOf((<-chan fswatcher.Event)(nil))
	}
	return reflect.TypeOf((chan fswatcher.Event)(nil))
}

func watcherErrorsType() reflect.Type {
	if supportedWatcherBackend(runtime.GOOS) {
		return reflect.TypeOf((<-chan error)(nil))
	}
	return reflect.TypeOf((chan error)(nil))
}

func supportedWatcherBackend(goos string) bool {
	switch goos {
	case "darwin", "freebsd", "linux", "windows":
		return true
	default:
		return false
	}
}

func assertExportedFields(t *testing.T, typ reflect.Type, want []struct {
	name string
	typ  reflect.Type
}) {
	t.Helper()

	got := make(map[string]reflect.Type)
	for i := range typ.NumField() {
		field := typ.Field(i)
		if field.IsExported() {
			got[field.Name] = field.Type
		}
	}
	if len(got) != len(want) {
		t.Fatalf("%s exported field count = %d (%v), want %d", typ, len(got), got, len(want))
	}
	for _, field := range want {
		gotType, ok := got[field.name]
		if !ok {
			t.Fatalf("%s missing exported field %q; exported fields: %v", typ, field.name, got)
		}
		if gotType != field.typ {
			t.Fatalf("%s.%s type = %s, want %s", typ, field.name, gotType, field.typ)
		}
	}
}

func assertExportedMethods(t *testing.T, typ reflect.Type, want []struct {
	name string
	typ  reflect.Type
}) {
	t.Helper()

	got := make(map[string]reflect.Type)
	for i := range typ.NumMethod() {
		method := typ.Method(i)
		got[method.Name] = method.Type
	}
	if len(got) != len(want) {
		t.Fatalf("%s exported method count = %d (%v), want %d", typ, len(got), got, len(want))
	}
	for _, method := range want {
		gotType, ok := got[method.name]
		if !ok {
			t.Fatalf("%s missing exported method %q; exported methods: %v", typ, method.name, got)
		}
		if gotType != method.typ {
			t.Fatalf("%s.%s type = %s, want %s", typ, method.name, gotType, method.typ)
		}
	}
}
