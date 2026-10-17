package liveeval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Anemiaaaa/HomeBudget-AI/internal/appbot"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/bot"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/expense"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/llm"
	postgresstore "github.com/Anemiaaaa/HomeBudget-AI/internal/storage/postgres"
	"github.com/Anemiaaaa/HomeBudget-AI/internal/telegram"
	"github.com/Anemiaaaa/HomeBudget-AI/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Provider interface {
	Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error)
}

type Options struct {
	DatabaseURL string
	ProviderURL string
	Model       string
	Commit      string
	Repeats     int
	CasesPath   string
	CaseIDs     []string
	Provider    Provider
}

type Report struct {
	GeneratedAt      time.Time               `json:"generated_at"`
	Provider         string                  `json:"provider"`
	Model            string                  `json:"model"`
	Commit           string                  `json:"commit"`
	PromptVersion    string                  `json:"prompt_version"`
	Repeats          int                     `json:"repeats"`
	TotalRuns        int                     `json:"total_runs"`
	PassedRuns       int                     `json:"passed_runs"`
	PassRate         float64                 `json:"pass_rate"`
	ByIntent         map[string]IntentResult `json:"by_intent"`
	LatencyP50Millis int64                   `json:"latency_p50_ms"`
	LatencyP95Millis int64                   `json:"latency_p95_ms"`
	Usage            llm.Usage               `json:"usage"`
	Cases            []CaseResult            `json:"cases"`
}

type IntentResult struct {
	Passed int `json:"passed"`
	Total  int `json:"total"`
}

type CaseResult struct {
	ID             string       `json:"id"`
	Intent         string       `json:"intent"`
	Repeat         int          `json:"repeat"`
	Passed         bool         `json:"passed"`
	DurationMillis int64        `json:"duration_ms"`
	LLMCalls       int          `json:"llm_calls"`
	ToolCalls      []string     `json:"tool_calls"`
	Usage          llm.Usage    `json:"usage"`
	Failures       []string     `json:"failures,omitempty"`
	Steps          []StepResult `json:"steps"`
}

type StepResult struct {
	Input            string   `json:"input"`
	Reply            string   `json:"reply"`
	Passed           bool     `json:"passed"`
	DurationMillis   int64    `json:"duration_ms"`
	LLMCalls         int      `json:"llm_calls"`
	ToolCalls        []string `json:"tool_calls"`
	PromptTokens     int      `json:"prompt_tokens"`
	CompletionTokens int      `json:"completion_tokens"`
	Failures         []string `json:"failures,omitempty"`
}

type caseSpec struct {
	ID            string        `json:"id"`
	Intent        string        `json:"intent"`
	ReferenceDate string        `json:"reference_date"`
	Initial       []expenseSpec `json:"initial"`
	Steps         []stepSpec    `json:"steps"`
}

type expenseSpec struct {
	Owner    string           `json:"owner"`
	Amount   string           `json:"amount"`
	Category expense.Category `json:"category"`
	SpentAt  string           `json:"spent_at"`
	Comment  string           `json:"comment"`
}

type stepSpec struct {
	Input              string        `json:"input"`
	CountA             *int          `json:"count_a"`
	CountB             *int          `json:"count_b"`
	ExpensesA          []expenseSpec `json:"expenses_a"`
	ExpensesB          []expenseSpec `json:"expenses_b"`
	ReplyContains      []string      `json:"reply_contains"`
	ReplyContainsOneOf []string      `json:"reply_contains_one_of"`
	ReplyExcludes      []string      `json:"reply_excludes"`
	Pending            string        `json:"pending"`
	MaxToolCalls       *int          `json:"max_tool_calls"`
}

type apiMeasurement struct {
	Duration  time.Duration
	Usage     llm.Usage
	ToolCalls []string
}

type measuredProvider struct {
	provider Provider
	calls    []apiMeasurement
}

func (p *measuredProvider) Chat(ctx context.Context, request llm.ChatRequest) (llm.ChatResponse, error) {
	started := time.Now()
	response, err := p.provider.Chat(ctx, request)
	measurement := apiMeasurement{Duration: time.Since(started), Usage: response.Usage}
	for _, call := range response.ToolCalls {
		measurement.ToolCalls = append(measurement.ToolCalls, call.Name)
	}
	p.calls = append(p.calls, measurement)
	return response, err
}

type discardSender struct{}

func (discardSender) SendMessage(context.Context, int64, string) (telegram.SentMessage, error) {
	return telegram.SentMessage{}, errors.New("live eval must not send Telegram messages")
}

func Run(ctx context.Context, options Options) (Report, error) {
	if options.DatabaseURL == "" || options.Model == "" || options.Provider == nil || options.Repeats < 1 {
		return Report{}, errors.New("live eval options are incomplete")
	}
	cases, err := loadCases(options.CasesPath)
	if err != nil {
		return Report{}, err
	}
	cases, err = filterCases(cases, options.CaseIDs)
	if err != nil {
		return Report{}, err
	}
	pool, err := pgxpool.New(ctx, options.DatabaseURL)
	if err != nil {
		return Report{}, fmt.Errorf("connect live eval database: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return Report{}, fmt.Errorf("ping live eval database: %w", err)
	}
	if err := postgresstore.Migrate(ctx, pool, migrations.FS); err != nil {
		return Report{}, err
	}

	provider := &measuredProvider{provider: options.Provider}
	report := Report{
		GeneratedAt: time.Now().UTC(), Provider: providerName(options.ProviderURL), Model: options.Model,
		Commit: fallback(options.Commit, "unknown"), PromptVersion: appbot.PromptVersion,
		Repeats: options.Repeats, ByIntent: make(map[string]IntentResult),
	}
	for repeat := 1; repeat <= options.Repeats; repeat++ {
		for _, spec := range cases {
			result := runCase(ctx, pool, provider, options.Model, spec, repeat)
			report.Cases = append(report.Cases, result)
			report.TotalRuns++
			intent := report.ByIntent[spec.Intent]
			intent.Total++
			if result.Passed {
				report.PassedRuns++
				intent.Passed++
			}
			report.ByIntent[spec.Intent] = intent
		}
	}
	if report.TotalRuns > 0 {
		report.PassRate = float64(report.PassedRuns) / float64(report.TotalRuns)
	}
	latencies := make([]time.Duration, 0, len(provider.calls))
	for _, call := range provider.calls {
		latencies = append(latencies, call.Duration)
		addUsage(&report.Usage, call.Usage)
	}
	report.LatencyP50Millis = percentile(latencies, 0.50).Milliseconds()
	report.LatencyP95Millis = percentile(latencies, 0.95).Milliseconds()
	_ = resetDatabase(context.Background(), pool)
	if err := acceptanceError(report); err != nil {
		return report, err
	}
	return report, nil
}

func acceptanceError(report Report) error {
	required := []string{"E01", "E02", "E05", "E06", "E08", "E11", "E12", "E13", "E14", "E15", "E21"}
	totals := make(map[string]int)
	passed := make(map[string]int)
	for _, result := range report.Cases {
		totals[result.ID]++
		if result.Passed {
			passed[result.ID]++
		}
	}
	fullAcceptanceSet := true
	for _, id := range required {
		if totals[id] == 0 {
			fullAcceptanceSet = false
			break
		}
	}
	if !fullAcceptanceSet {
		if report.PassedRuns != report.TotalRuns {
			return fmt.Errorf("live eval failed: %d/%d selected runs passed", report.PassedRuns, report.TotalRuns)
		}
		return nil
	}
	if report.PassRate < 0.90 {
		return fmt.Errorf("live eval acceptance failed: %.2f%% passed, need at least 90%%", report.PassRate*100)
	}
	for _, id := range required {
		if passed[id] != totals[id] {
			return fmt.Errorf("live eval acceptance failed: required case %s passed %d/%d runs", id, passed[id], totals[id])
		}
	}
	return nil
}

func runCase(ctx context.Context, pool *pgxpool.Pool, provider *measuredProvider, model string, spec caseSpec, repeat int) CaseResult {
	started := time.Now()
	result := CaseResult{ID: spec.ID, Intent: spec.Intent, Repeat: repeat, Passed: true}
	if err := resetDatabase(ctx, pool); err != nil {
		result.Passed = false
		result.Failures = append(result.Failures, err.Error())
		return result
	}
	reference, err := expense.ParseDate(fallback(spec.ReferenceDate, "2026-10-15"))
	if err != nil {
		result.Passed = false
		result.Failures = append(result.Failures, "invalid fixture reference date")
		return result
	}
	repository := postgresstore.NewExpenseRepository(pool)
	owners := expense.NewOwnerService(repository)
	for _, item := range spec.Initial {
		comment := optionalComment(item.Comment)
		if _, err := owners.Create(ctx, ownerID(item.Owner), expense.CreateFields{
			Amount: item.Amount, Category: item.Category, SpentAt: item.SpentAt, Comment: comment,
		}, reference); err != nil {
			result.Passed = false
			result.Failures = append(result.Failures, "fixture: "+err.Error())
			return result
		}
	}
	store := postgresstore.NewBotRepository(pool)
	processor := appbot.NewProcessor(store, provider, owners, discardSender{}, model)
	caseCallStart := len(provider.calls)
	for index, step := range spec.Steps {
		stepStarted := time.Now()
		stepCallStart := len(provider.calls)
		updateID := int64(index + 1)
		messageID := updateID
		messageAt := time.Date(reference.Year(), reference.Month(), reference.Day(), 12, 0, 0, 0, time.UTC)
		text := step.Input
		_, ingestErr := store.Ingest(ctx, []bot.IncomingUpdate{{
			UpdateID: updateID, UserID: int64Pointer(ownerID("A")), ChatID: int64Pointer(501), MessageID: &messageID,
			MessageAt: &messageAt, ReferenceDate: &reference, Text: &text, Status: bot.StatusPending,
		}}, updateID+1)
		if ingestErr == nil {
			turnCtx, cancel := context.WithTimeout(ctx, time.Minute)
			ingestErr = processor.ProcessOne(turnCtx)
			cancel()
		}
		stepResult := StepResult{Input: step.Input, Passed: ingestErr == nil, DurationMillis: time.Since(stepStarted).Milliseconds()}
		if ingestErr != nil {
			stepResult.Failures = append(stepResult.Failures, ingestErr.Error())
		} else {
			stored, getErr := store.Get(ctx, updateID)
			if getErr != nil || stored.ReplyText == nil {
				stepResult.Passed = false
				stepResult.Failures = append(stepResult.Failures, "completed update has no reply")
			} else {
				stepResult.Reply = *stored.ReplyText
				validateStep(ctx, pool, store, step, &stepResult)
			}
		}
		measurements := provider.calls[stepCallStart:]
		stepResult.LLMCalls = len(measurements)
		for _, measurement := range measurements {
			stepResult.ToolCalls = append(stepResult.ToolCalls, measurement.ToolCalls...)
			stepResult.PromptTokens += measurement.Usage.PromptTokens
			stepResult.CompletionTokens += measurement.Usage.CompletionTokens
		}
		if step.MaxToolCalls != nil && len(stepResult.ToolCalls) > *step.MaxToolCalls {
			stepResult.Passed = false
			stepResult.Failures = append(stepResult.Failures, fmt.Sprintf("tool calls %d exceed %d", len(stepResult.ToolCalls), *step.MaxToolCalls))
		}
		if !stepResult.Passed {
			result.Passed = false
			result.Failures = append(result.Failures, fmt.Sprintf("step %d failed", index+1))
		}
		result.Steps = append(result.Steps, stepResult)
	}
	for _, measurement := range provider.calls[caseCallStart:] {
		result.LLMCalls++
		result.ToolCalls = append(result.ToolCalls, measurement.ToolCalls...)
		addUsage(&result.Usage, measurement.Usage)
	}
	result.DurationMillis = time.Since(started).Milliseconds()
	return result
}

func validateStep(ctx context.Context, pool *pgxpool.Pool, store *postgresstore.BotRepository, spec stepSpec, result *StepResult) {
	for _, expected := range spec.ReplyContains {
		if !strings.Contains(strings.ToLower(result.Reply), strings.ToLower(expected)) {
			failStep(result, fmt.Sprintf("reply does not contain %q", expected))
		}
	}
	if len(spec.ReplyContainsOneOf) > 0 {
		matched := false
		for _, expected := range spec.ReplyContainsOneOf {
			if strings.Contains(strings.ToLower(result.Reply), strings.ToLower(expected)) {
				matched = true
				break
			}
		}
		if !matched {
			failStep(result, fmt.Sprintf("reply does not contain any of %q", spec.ReplyContainsOneOf))
		}
	}
	for _, forbidden := range spec.ReplyExcludes {
		if strings.Contains(strings.ToLower(result.Reply), strings.ToLower(forbidden)) {
			failStep(result, fmt.Sprintf("reply contains forbidden %q", forbidden))
		}
	}
	validateOwner := func(owner string, expectedCount *int, expected []expenseSpec) {
		actual, err := readExpenses(ctx, pool, ownerID(owner))
		if err != nil {
			failStep(result, err.Error())
			return
		}
		if expectedCount != nil && len(actual) != *expectedCount {
			failStep(result, fmt.Sprintf("owner %s expense count is %d, want %d", owner, len(actual), *expectedCount))
		}
		if expected != nil {
			want := expenseSignatures(expected)
			if strings.Join(actual, "\n") != strings.Join(want, "\n") {
				failStep(result, fmt.Sprintf("owner %s expenses are %v, want %v", owner, actual, want))
			}
		}
	}
	validateOwner("A", spec.CountA, spec.ExpensesA)
	validateOwner("B", spec.CountB, spec.ExpensesB)
	if spec.Pending != "" {
		session, err := store.LoadSession(ctx, ownerID("A"), 501)
		if err != nil {
			failStep(result, err.Error())
			return
		}
		kind := "none"
		if len(session.PendingAction) > 0 {
			var value struct {
				Kind string `json:"kind"`
			}
			if err := json.Unmarshal(session.PendingAction, &value); err != nil {
				failStep(result, "pending action is invalid")
				return
			}
			kind = value.Kind
		}
		if kind != spec.Pending {
			failStep(result, fmt.Sprintf("pending action is %q, want %q", kind, spec.Pending))
		}
	}
}

func loadCases(path string) ([]caseSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read live eval cases: %w", err)
	}
	var cases []caseSpec
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, fmt.Errorf("decode live eval cases: %w", err)
	}
	if len(cases) == 0 {
		return nil, errors.New("live eval case list is empty")
	}
	for _, spec := range cases {
		if spec.ID == "" || spec.Intent == "" || len(spec.Steps) == 0 {
			return nil, fmt.Errorf("live eval case is incomplete: %q", spec.ID)
		}
	}
	return cases, nil
}

func filterCases(cases []caseSpec, rawIDs []string) ([]caseSpec, error) {
	wanted := make(map[string]struct{})
	for _, rawID := range rawIDs {
		if id := strings.TrimSpace(rawID); id != "" {
			wanted[id] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return cases, nil
	}
	selected := make([]caseSpec, 0, len(wanted))
	for _, spec := range cases {
		if _, exists := wanted[spec.ID]; exists {
			selected = append(selected, spec)
			delete(wanted, spec.ID)
		}
	}
	if len(wanted) > 0 {
		missing := make([]string, 0, len(wanted))
		for id := range wanted {
			missing = append(missing, id)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("live eval cases not found: %s", strings.Join(missing, ", "))
	}
	return selected, nil
}

func resetDatabase(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `TRUNCATE bot_updates, bot_sessions, expenses, users RESTART IDENTITY CASCADE;
		UPDATE bot_polling_state SET next_offset = 0, updated_at = now() WHERE singleton = TRUE`)
	return err
}

func readExpenses(ctx context.Context, pool *pgxpool.Pool, userID int64) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT amount::text, category, spent_at::text, coalesce(comment, '')
		FROM expenses WHERE user_id = $1 ORDER BY amount, category, spent_at, comment`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var amount, category, spentAt, comment string
		if err := rows.Scan(&amount, &category, &spentAt, &comment); err != nil {
			return nil, err
		}
		result = append(result, strings.Join([]string{amount, category, spentAt, comment}, "|"))
	}
	return result, rows.Err()
}

func expenseSignatures(items []expenseSpec) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		amount, err := expense.ParseMoney(item.Amount)
		if err != nil {
			result = append(result, "invalid fixture amount")
			continue
		}
		result = append(result, strings.Join([]string{amount.String(), string(item.Category), item.SpentAt, item.Comment}, "|"))
	}
	sort.Strings(result)
	return result
}

func ownerID(owner string) int64 {
	if strings.EqualFold(owner, "B") {
		return 202
	}
	return 101
}

func optionalComment(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func int64Pointer(value int64) *int64 { return &value }

func failStep(result *StepResult, message string) {
	result.Passed = false
	result.Failures = append(result.Failures, message)
}

func addUsage(target *llm.Usage, value llm.Usage) {
	target.PromptTokens += value.PromptTokens
	target.CompletionTokens += value.CompletionTokens
	target.TotalTokens += value.TotalTokens
}

func percentile(values []time.Duration, quantile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]time.Duration(nil), values...)
	sort.Slice(copyValues, func(left, right int) bool { return copyValues[left] < copyValues[right] })
	index := int(float64(len(copyValues)-1) * quantile)
	return copyValues[index]
}

func providerName(raw string) string {
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return "openai-compatible"
}

func fallback(value, fallbackValue string) string {
	if strings.TrimSpace(value) == "" {
		return fallbackValue
	}
	return strings.TrimSpace(value)
}
