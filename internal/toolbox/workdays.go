package toolbox

import (
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var chineseDatePattern = regexp.MustCompile(
	`^(?:([0-9]{4})[[:space:]]*年[[:space:]]*)?` +
		`([0-9]{1,2})[[:space:]]*月[[:space:]]*` +
		`([0-9]{1,2})[[:space:]]*[日号]?[[:space:]]*$`,
)

func workdays(input io.Reader, output io.Writer) error {
	var arguments struct {
		Start string `json:"start"`
		End   string `json:"end"`
	}
	if err := decode(input, &arguments); err != nil {
		return err
	}
	start, err := parseDate(arguments.Start, time.Now())
	if err != nil {
		return errors.New("invalid input: " + err.Error() + "; expect start/end as YYYY-MM-DD")
	}
	end, err := parseDate(arguments.End, time.Now())
	if err != nil {
		return errors.New("invalid input: " + err.Error() + "; expect start/end as YYYY-MM-DD")
	}
	if end.Before(start) {
		start, end = end, start
	}
	workdayCount, weekendCount, total := 0, 0, 0
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		total++
		if day.Weekday() >= time.Monday && day.Weekday() <= time.Friday {
			workdayCount++
		} else {
			weekendCount++
		}
	}
	return writeJSON(output, map[string]any{
		"workdays":     workdayCount,
		"weekend_days": weekendCount,
		"total_days":   total,
		"summary": "共 " + decimal(total) + " 天，其中工作日 " +
			decimal(workdayCount) + " 天、周末 " + decimal(weekendCount) + " 天",
	})
}

func parseDate(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if parsed, err := time.ParseInLocation("2006-01-02", value, now.Location()); err == nil {
		return parsed, nil
	}
	parts := chineseDatePattern.FindStringSubmatch(value)
	if parts == nil {
		return time.Time{}, errors.New("unrecognized date " + strconv.Quote(value))
	}
	year := now.Year()
	var err error
	if parts[1] != "" {
		year, err = strconv.Atoi(parts[1])
		if err != nil {
			return time.Time{}, err
		}
	}
	month, _ := strconv.Atoi(parts[2])
	day, _ := strconv.Atoi(parts[3])
	parsed := time.Date(year, time.Month(month), day, 0, 0, 0, 0, now.Location())
	if parsed.Year() != year || int(parsed.Month()) != month || parsed.Day() != day {
		return time.Time{}, errors.New("unrecognized date " + strconv.Quote(value))
	}
	return parsed, nil
}
