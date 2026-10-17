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

func TestAcceptanceMatchesSDDThreshold(t *testing.T) {
	t.Parallel()
	required := []string{"E01", "E02", "E05", "E06", "E08", "E11", "E12", "E13", "E14", "E15", "E21"}
	report := Report{TotalRuns: 57, PassedRuns: 55, PassRate: 55.0 / 57.0}
	for _, id := range required {
		for repeat := 1; repeat <= 3; repeat++ {
			report.Cases = append(report.Cases, CaseResult{ID: id, Repeat: repeat, Passed: true})
		}
	}
	if err := acceptanceError(report); err != nil {
		t.Fatalf("passing SDD report was rejected: %v", err)
	}
	report.Cases[0].Passed = false
	if err := acceptanceError(report); err == nil {
		t.Fatal("failed required case was accepted")
	}
}

func TestAcceptanceRequiresAllSelectedCasesForPartialRun(t *testing.T) {
	t.Parallel()
	report := Report{
		TotalRuns: 3, PassedRuns: 2, PassRate: 2.0 / 3.0,
		Cases: []CaseResult{{ID: "E04", Passed: true}, {ID: "E04", Passed: true}, {ID: "E04", Passed: false}},
	}
	if err := acceptanceError(report); err == nil {
		t.Fatal("failed partial eval was accepted")
	}
}
