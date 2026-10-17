package agent

type Definition struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

type FunctionDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

func Definitions() []Definition {
	category := map[string]any{"type": "string", "enum": []string{"food", "transport", "housing", "entertainment", "health", "shopping", "other"}}
	identifier := map[string]any{"type": "string", "pattern": `^[1-9][0-9]*$`}
	money := map[string]any{"type": "string", "pattern": `^(0|[1-9][0-9]*)(\.[0-9]{1,2})?$`}
	date := map[string]any{"type": "string", "description": "YYYY-MM-DD; create/update also accept Russian relative dates"}
	patch := object(map[string]any{
		"amount": money, "category": category, "spent_at": date,
		"comment": map[string]any{"type": []string{"string", "null"}, "maxLength": 500},
	})
	createExpense := object(
		map[string]any{"amount": money, "category": category, "spent_at": date, "comment": map[string]any{"type": "string", "maxLength": 500}},
		"amount", "category", "spent_at",
	)
	draft := object(map[string]any{
		"amount": money, "category": category, "spent_at": date,
		"comment":             map[string]any{"type": "string", "maxLength": 500},
		"original_request":    map[string]any{"type": "string", "minLength": 1, "maxLength": 1000},
		"intent":              map[string]any{"type": "string", "enum": []string{"update", "delete"}},
		"expected_old_amount": money,
		"patch":               patch,
	})

	return []Definition{
		function("create_expense", "Create one expense for the authenticated owner", createExpense),
		function("create_expenses", "Atomically create 2 to 10 independent expenses for the authenticated owner", object(
			map[string]any{"expenses": map[string]any{"type": "array", "minItems": 2, "maxItems": 10, "items": createExpense}},
			"expenses",
		)),
		function("list_expenses", "Read only the authenticated owner's expenses before answering or changing them. For 'last/latest expense', use only order=created_at_desc and limit=1 without filters", object(
			map[string]any{
				"expense_id": identifier, "category": category, "date_from": date, "date_to": date,
				"query":  map[string]any{"type": "string", "maxLength": 200},
				"order":  map[string]any{"type": "string", "enum": []string{"spent_at_desc", "created_at_desc"}},
				"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
				"offset": map[string]any{"type": "integer", "minimum": 0},
			},
		)),
		function("summarize_expenses", "Calculate an exact total over all matching expenses for the authenticated owner", object(
			map[string]any{
				"category": category, "date_from": date, "date_to": date,
				"query": map[string]any{"type": "string", "maxLength": 200},
			},
		)),
		function("update_expense", "Update one expense that was read in this turn", object(
			map[string]any{
				"expense_id": identifier, "expected_version": identifier,
				"expected_old_amount": money,
				"patch":               patch,
			},
			"expense_id", "expected_version", "patch",
		)),
		function("delete_expense", "Prepare confirmation for deleting one expense that was read in this turn; does not delete immediately", object(
			map[string]any{"expense_id": identifier, "expected_version": identifier},
			"expense_id", "expected_version",
		)),
		function("ask_clarification", "Save a structured pending question when the request is incomplete or ambiguous", object(
			map[string]any{
				"kind":          map[string]any{"type": "string", "enum": []string{"clarify_create", "clarify_request", "choose_target"}},
				"question":      map[string]any{"type": "string", "minLength": 1, "maxLength": 500},
				"draft":         draft,
				"candidate_ids": map[string]any{"type": "array", "maxItems": 5, "items": identifier},
			},
			"kind", "question", "draft",
		)),
	}
}

func function(name, description string, parameters map[string]any) Definition {
	return Definition{Type: "function", Function: FunctionDefinition{Name: name, Description: description, Parameters: parameters}}
}

func object(properties map[string]any, required ...string) map[string]any {
	result := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
	}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}
