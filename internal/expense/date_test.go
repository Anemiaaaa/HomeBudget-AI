package expense

import (
	"testing"
	"time"
)

func TestDateRules(t *testing.T) {
	t.Parallel()
	reference, _ := ParseDate("2026-10-15")

	yesterday, err := ResolveRelativeDate("вчера", reference)
	if err != nil || yesterday.String() != "2026-10-14" {
		t.Fatalf("yesterday = %s, %v", yesterday, err)
	}
	week := LastSevenDays(reference)
	if week.From.String() != "2026-10-09" || week.To.String() != "2026-10-15" {
		t.Fatalf("last seven days = %s..%s", week.From, week.To)
	}
	calendarWeek := CurrentWeek(reference)
	if calendarWeek.From.String() != "2026-10-12" {
		t.Fatalf("current week starts %s", calendarWeek.From)
	}
	month := CurrentMonth(reference)
	if month.From.String() != "2026-10-01" {
		t.Fatalf("current month starts %s", month.From)
	}
}

func TestDateInLocationNearMidnight(t *testing.T) {
	t.Parallel()
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	instant := time.Date(2026, 10, 15, 21, 30, 0, 0, time.UTC)
	if got := DateInLocation(instant, location).String(); got != "2026-10-16" {
		t.Fatalf("DateInLocation() = %s", got)
	}
}
