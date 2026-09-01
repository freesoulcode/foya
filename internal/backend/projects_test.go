package backend

import (
	"reflect"
	"testing"

	"github.com/freesoulcode/foya/internal/session"
)

func TestProjectSessionRoots(t *testing.T) {
	items := []*session.Session{
		{ID: "root", ProjectID: "project-a"},
		{ID: "child", ParentID: "root", ProjectID: "project-a"},
		{ID: "other-root", ProjectID: "project-b"},
		{ID: "other-child", ParentID: "other-root", ProjectID: "project-a"},
	}
	got := projectSessionRoots(items, "project-a")
	want := []string{"root", "other-child"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("project session roots = %#v, want %#v", got, want)
	}
}
