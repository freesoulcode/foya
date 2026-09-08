package kernel

import (
	"reflect"
	"testing"

	conversation "github.com/freesoulcode/foya/internal/conversation"
)

func TestProjectSessionRoots(t *testing.T) {
	items := []*conversation.Session{
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
