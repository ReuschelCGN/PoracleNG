package api

import "testing"

// hasFieldDef reports whether fields contains an entry with the given name.
func hasFieldDef(fields []FieldDef, name string) bool {
	for _, f := range fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

func TestMonsterFields_Costume(t *testing.T) {
	m := fieldsByType["monster"]
	if !hasFieldDef(m.Fields, "costumeName") {
		t.Error("monster type should list costumeName")
	}
}
