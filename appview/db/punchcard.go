package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"tangled.org/core/appview/models"
)

// this adds to the existing count
func AddPunch(e Execer, punch models.Punch) error {
	_, err := e.Exec(`
		insert into punchcard (did, date, count)
		values (?, ?, ?)
			on conflict(did, date) do update set
			count = coalesce(punchcard.count, 0) + excluded.count;
	`, punch.Did, punch.Date.Format(time.DateOnly), punch.Count)
	return err
}

func MakePunchcard(e Execer, filters ...filter) (*models.Punchcard, error) {
	punchcard := &models.Punchcard{}
	now := time.Now()
	startOfYear := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	endOfYear := time.Date(now.Year(), 12, 31, 0, 0, 0, 0, time.UTC)
	for d := startOfYear; d.Before(endOfYear) || d.Equal(endOfYear); d = d.AddDate(0, 0, 1) {
		punchcard.Punches = append(punchcard.Punches, models.Punch{
			Date:  d,
			Count: 0,
		})
	}

	var conditions []string
	var args []any
	for _, filter := range filters {
		conditions = append(conditions, filter.Condition())
		args = append(args, filter.Arg()...)
	}

	whereClause := ""
	if conditions != nil {
		whereClause = " where " + strings.Join(conditions, " and ")
	}

	query := fmt.Sprintf(`
		select date, sum(count) as total_count
		from punchcard
		%s
		group by date
		order by date
		`, whereClause)

	rows, err := e.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var punch models.Punch
		var date string
		var count sql.NullInt64
		if err := rows.Scan(&date, &count); err != nil {
			return nil, err
		}

		punch.Date, err = time.Parse(time.DateOnly, date)
		if err != nil {
			fmt.Println("invalid date")
			// this punch is not recorded if date is invalid
			continue
		}

		if count.Valid {
			punch.Count = int(count.Int64)
		}

		punchcard.Punches[punch.Date.YearDay()] = punch
		punchcard.Total += punch.Count
	}

	return punchcard, nil
}
