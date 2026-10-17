package appbot

import (
	"strings"
	"testing"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/agent"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/llm"
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

func TestNewRequestDetection(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"добавь кофе 300", "сколько потратил", "кофе 300", "удали такси"} {
		if !looksLikeNewRequest(normalizeAnswer(input)) {
			t.Errorf("%q was not detected as a new request", input)
		}
	}
	for _, input := range []string{"может быть", "не уверен", "первое"} {
		if looksLikeNewRequest(normalizeAnswer(input)) {
			t.Errorf("%q was unexpectedly detected as a new request", input)
		}
	}
}

func TestTrimHistoryDropsOldestPairsOnly(t *testing.T) {
	t.Parallel()
	definitions := agent.Definitions()
	messages := []llm.Message{
		{Role: "system", Content: "rules"},
		{Role: "user", Content: strings.Repeat("old", 200)},
		{Role: "assistant", Content: strings.Repeat("reply", 200)},
		{Role: "user", Content: "recent"},
		{Role: "assistant", Content: "recent reply"},
		{Role: "user", Content: "current"},
	}
	afterOne := append(append([]llm.Message{}, messages[:1]...), messages[3:]...)
	limit := (contextSize(messages, definitions) + contextSize(afterOne, definitions)) / 2
	trimmed, count := trimHistoryToFit(messages, 4, definitions, limit)
	if count != 2 || len(trimmed) != 4 {
		t.Fatalf("trimmed messages = %+v, history count = %d", trimmed, count)
	}
	if trimmed[1].Content != "recent" || trimmed[len(trimmed)-1].Content != "current" {
		t.Fatalf("wrong messages survived trimming: %+v", trimmed)
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

func TestNormalizeTerminalCallAsksForMissingAmount(t *testing.T) {
	t.Parallel()
	call := normalizeToolCall("кофе", nil, llm.ToolCall{
		Name: "create_expense", Arguments: []byte(`{"category":"food","spent_at":"2026-10-15","comment":"кофе"}`),
	})
	if call.Name != "ask_clarification" || !strings.Contains(string(call.Arguments), "clarify_create") || !strings.Contains(string(call.Arguments), "Укажите сумму") {
		t.Fatalf("normalized call = %+v", call)
	}
}

func TestNormalizeTerminalCallRepairsCompositeClarification(t *testing.T) {
	t.Parallel()
	call := normalizeToolCall("кофе 100 и такси 200", nil, llm.ToolCall{
		Name: "ask_clarification", Arguments: []byte(`{"kind":"clarify_create","question":"Что добавить?","draft":{"comment":"кофе"}}`),
	})
	if !strings.Contains(string(call.Arguments), "один расход") || !strings.Contains(string(call.Arguments), "original_request") {
		t.Fatalf("normalized call arguments = %s", call.Arguments)
	}
}

func TestNormalizeToolCallReadsLatestByCreationOrder(t *testing.T) {
	t.Parallel()
	call := normalizeToolCall("последнее было не 200, а 250", nil, llm.ToolCall{
		Name: "list_expenses", Arguments: []byte(`{"query":"200","order":"spent_at_desc","limit":20}`),
	})
	if string(call.Arguments) != `{"order":"created_at_desc","limit":1}` {
		t.Fatalf("normalized call arguments = %s", call.Arguments)
	}
}

func TestNormalizeToolCallRepairsDeleteChoice(t *testing.T) {
	t.Parallel()
	turn := &agent.Turn{Reads: map[int64]agent.ReadSnapshot{
		1: {ID: 1, SpentAt: "2026-10-14"}, 2: {ID: 2, SpentAt: "2026-10-15"},
	}}
	call := normalizeToolCall("удали такси", turn, llm.ToolCall{
		Name: "ask_clarification", Arguments: []byte(`{"kind":"choose_target","question":"Какой?","draft":{},"candidate_ids":[2,1]}`),
	})
	for _, want := range []string{`"intent":"delete"`, `"candidate_ids":["2","1"]`} {
		if !strings.Contains(string(call.Arguments), want) {
			t.Fatalf("normalized call arguments = %s; want %s", call.Arguments, want)
		}
	}
}

func TestKnownRequestDetection(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"сколько потратил", "Сколько всего я потратил?"} {
		if !isAllTimeSummaryRequest(input) {
			t.Errorf("%q was not detected as all-time summary", input)
		}
	}
	if isAllTimeSummaryRequest("сколько потратил на еду") {
		t.Fatal("filtered summary was detected as all-time summary")
	}
	if !isCompositeExpenseRequest("кофе 100 и такси 200") {
		t.Fatal("composite expense request was not detected")
	}
}
