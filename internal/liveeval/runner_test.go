package liveeval

import "testing"

func TestFilterCases(t *testing.T) {
	t.Parallel()
	cases := []caseSpec{{ID: "E01"}, {ID: "E04"}, {ID: "E21"}}

	selected, err := filterCases(cases, []string{"E21", " E04 "})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 || selected[0].ID != "E04" || selected[1].ID != "E21" {
		t.Fatalf("selected cases = %+v", selected)
	}

	if _, err := filterCases(cases, []string{"E99"}); err == nil {
		t.Fatal("missing case was accepted")
	}
}
