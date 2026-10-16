package appbot

import (
	"strings"
	"testing"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/agent"
)

func TestSelectedCandidateUsesSavedOrder(t *testing.T) {
	t.Parallel()
	candidates := []agent.ReadSnapshot{{ID: 41}, {ID: 17}, {ID: 99}}

	tests := []struct {
		answer string
		wantID int64
	}{
		{"первое", 41},
		{"второе", 17},
		{"3", 99},
		{"номер 17", 17},
		{"id 99", 99},
	}
	for _, test := range tests {
		candidate, ok := selectedCandidate(test.answer, candidates)
		if !ok || candidate.ID != test.wantID {
			t.Fatalf("selectedCandidate(%q) = %+v, %v; want ID %d", test.answer, candidate, ok, test.wantID)
		}
	}
	if _, ok := selectedCandidate("последнее", candidates); ok {
		t.Fatal("unsupported selection must not choose a candidate")
	}
}

func TestChoiceReplyUsesSavedCandidateFacts(t *testing.T) {
	t.Parallel()
	comment := "такси"
	result := agent.Result{Name: "ask_clarification", Payload: map[string]any{
		"kind":     "choose_target",
		"question": "Я уже удалил всё",
		"candidates": []map[string]any{
			{"id": 41, "amount": "900.00", "category": "transport", "spent_at": "2026-10-14", "comment": comment, "version": 1},
			{"id": 17, "amount": "1200.00", "category": "transport", "spent_at": "2026-10-15", "comment": comment, "version": 2},
		},
	}}

	_, reply, err := resultAndReply(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1. 14.10.2026 — 900,00 ₽, такси, №41", "2. 15.10.2026 — 1200,00 ₽, такси, №17"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply %q does not contain %q", reply, want)
		}
	}
	if strings.Contains(reply, "уже удалил") {
		t.Fatalf("reply contains untrusted model claim: %q", reply)
	}
}
