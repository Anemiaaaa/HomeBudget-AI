package expense

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const dateLayout = "2006-01-02"

type Date struct {
	time.Time
}

type Period struct {
	From Date
	To   Date
}

func ParseDate(value string) (Date, error) {
	parsed, err := time.Parse(dateLayout, value)
	if err != nil {
		return Date{}, fmt.Errorf("date must use YYYY-MM-DD: %w", err)
	}
	return Date{Time: parsed}, nil
}

func DateInLocation(instant time.Time, location *time.Location) Date {
	local := instant.In(location)
	return Date{Time: time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)}
}

func (d Date) String() string { return d.Time.Format(dateLayout) }

func (d Date) AddDays(days int) Date { return Date{Time: d.Time.AddDate(0, 0, days)} }

func (d Date) After(other Date) bool { return d.Time.After(other.Time) }

func (d Date) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

func ResolveRelativeDate(value string, reference Date) (Date, error) {
	switch value {
	case "today", "сегодня":
		return reference, nil
	case "yesterday", "вчера":
		return reference.AddDays(-1), nil
	default:
		return ParseDate(value)
	}
}

func LastSevenDays(reference Date) Period {
	return Period{From: reference.AddDays(-6), To: reference}
}

func CurrentWeek(reference Date) Period {
	weekday := int(reference.Time.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	return Period{From: reference.AddDays(-(weekday - 1)), To: reference}
}

func CurrentMonth(reference Date) Period {
	return Period{
		From: Date{Time: time.Date(reference.Year(), reference.Month(), 1, 0, 0, 0, 0, time.UTC)},
		To:   reference,
	}
}

func ValidatePeriod(period Period) error {
	if period.From.After(period.To) {
		return errors.New("date_from must not be after date_to")
	}
	return nil
}
