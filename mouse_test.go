package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"main.go/internal/driver"
)

func TestTabAtColumn(t *testing.T) {
	m := Model{}
	// Labels: "Connections"(11)+4=15, "Schema"(6)+4=10, "Query"(5)+4=9.
	cases := []struct {
		x    int
		want Tab
		ok   bool
	}{
		{0, TabConnections, true},
		{14, TabConnections, true},
		{15, TabSchema, true},
		{24, TabSchema, true},
		{25, TabQuery, true},
		{33, TabQuery, true},
		{34, 0, false},
	}
	for _, c := range cases {
		got, ok := m.tabAtColumn(c.x)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("tabAtColumn(%d) = (%v,%v), want (%v,%v)", c.x, got, ok, c.want, c.ok)
		}
	}
}

func TestSchemaObjectRowMap(t *testing.T) {
	m := Model{objects: []driver.DBObject{
		{Schema: "main", Name: "a"},
		{Schema: "main", Name: "b"},
		{Schema: "other", Name: "c"},
	}}
	// With ample visible height, layout is:
	//   row 0: header "main"
	//   row 1: a (obj 0)
	//   row 2: b (obj 1)
	//   row 3: header "other"
	//   row 4: c (obj 2)
	visible := 20
	if idx, ok := m.objectIndexAtRow(visible, 4); !ok || idx != 2 {
		t.Errorf("objectIndexAtRow(4) = (%d,%v), want (2,true)", idx, ok)
	}
	if idx, ok := m.objectIndexAtRow(visible, 1); !ok || idx != 0 {
		t.Errorf("objectIndexAtRow(1) = (%d,%v), want (0,true)", idx, ok)
	}
	if _, ok := m.objectIndexAtRow(visible, 0); ok {
		t.Error("row 0 is a header, should not map to an object")
	}
	if _, ok := m.objectIndexAtRow(visible, 3); ok {
		t.Error("row 3 is a header, should not map to an object")
	}
}

func TestSchemaWindowKeepsSelectionVisible(t *testing.T) {
	// 50 objects in one schema, small window — selection near the end must be
	// present in the rendered lines.
	var objs []driver.DBObject
	for i := 0; i < 50; i++ {
		objs = append(objs, driver.DBObject{Schema: "main", Name: "t" + itoaSimple(i), Kind: driver.KindTable})
	}
	m := Model{objects: objs, schemaCursor: 45}
	visible := 10
	lines := m.schemaListLines(m.filteredObjects(), visible)
	if len(lines) > visible {
		t.Fatalf("rendered %d lines, exceeds visible %d", len(lines), visible)
	}
	found := false
	for _, l := range lines {
		if strings.Contains(l, "t45") {
			found = true
		}
	}
	if !found {
		t.Fatalf("selected item t45 not in visible window: %v", lines)
	}
}

func TestSchemaFilter(t *testing.T) {
	m := Model{objects: []driver.DBObject{
		{Schema: "main", Name: "users", Kind: driver.KindTable},
		{Schema: "main", Name: "orders", Kind: driver.KindTable},
		{Schema: "main", Name: "user_sessions", Kind: driver.KindTable},
	}, filter: "user"}
	got := m.filteredObjects()
	if len(got) != 2 {
		t.Fatalf("filter 'user' matched %d, want 2: %v", len(got), got)
	}
}

func TestWheelScrollsConnectionList(t *testing.T) {
	m := Model{
		tab: TabConnections,
		conns: []connItem{
			{info: driver.ConnInfo{Name: "a"}},
			{info: driver.ConnInfo{Name: "b"}},
			{info: driver.ConnInfo{Name: "c"}},
		},
	}
	// Wheel down moves the cursor down.
	down := tea.MouseWheelMsg{Button: tea.MouseWheelDown}
	model, _ := m.handleMouse(down)
	m2 := model.(Model)
	if m2.connCursor != 1 {
		t.Fatalf("after wheel down, connCursor = %d, want 1", m2.connCursor)
	}
}

func TestClickTabBarSwitchesTab(t *testing.T) {
	m := Model{tab: TabConnections, cfg: defaultConfig()}
	click := tea.MouseClickMsg{Button: tea.MouseLeft, X: 16, Y: rowTabBar} // Schema label
	model, _ := m.handleMouse(click)
	if got := model.(Model).tab; got != TabSchema {
		t.Fatalf("clicking Schema tab => tab %v, want TabSchema", got)
	}
}
