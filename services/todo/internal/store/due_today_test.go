package store_test

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/fsociety/todo/pkg/tz"
	"github.com/fsociety/todo/services/todo/internal/store"
)

// "Today" follows the caller's zone: the same instants fall on different days per zone.
func TestListTasks_DueTodayUsesTimeZone(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	zones := []string{"Asia/Shanghai", "America/Los_Angeles", "UTC"}

	type due struct {
		id string
		at time.Time
	}
	var dues []due
	for _, z := range zones {
		loc := tz.Load(z)
		y, m, d := time.Now().In(loc).Date()
		for _, off := range []struct {
			name      string
			day, h, m int
		}{{"yesterday-late", -1, 23, 59}, {"today-early", 0, 0, 1}, {"today-late", 0, 23, 58}, {"tomorrow-early", 1, 0, 1}} {
			dues = append(dues, due{"dt-" + z + "-" + off.name, time.Date(y, m, d+off.day, off.h, off.m, 0, 0, loc)})
		}
	}
	for _, d := range dues {
		task := newTask(d.id, "u-tz", nil, nil)
		at := d.at.UTC()
		task.DueAt = &at
		if err := s.CreateTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}

	for _, z := range append(zones, "", "Not/AZone") {
		loc := tz.Load(z) // invalid zones fall back to UTC
		y, m, d := time.Now().In(loc).Date()
		var want []string
		for _, du := range dues {
			if yy, mm, dd := du.at.In(loc).Date(); yy == y && mm == m && dd == d {
				want = append(want, du.id)
			}
		}
		got, err := s.ListTasks(ctx, store.ListFilter{UserID: "u-tz", DueTodayOnly: true, TimeZone: z, PageSize: 200})
		if err != nil {
			t.Fatalf("ListTasks(%q): %v", z, err)
		}
		var ids []string
		for _, tk := range got {
			ids = append(ids, tk.ID)
		}
		sort.Strings(ids)
		sort.Strings(want)
		if !reflect.DeepEqual(ids, want) {
			t.Errorf("zone %q: due today = %v, want %v", z, ids, want)
		}
	}
}
