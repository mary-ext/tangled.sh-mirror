package db

import (
	"fmt"

	"tangled.sh/tangled.sh/core/knotserver/notifier"
)

type Event struct {
	Rkey      string `json:"rkey"`
	Nsid      string `json:"nsid"`
	EventJson string `json:"event"`
}

func (d *DB) InsertEvent(event Event, notifier *notifier.Notifier) error {
	_, err := d.db.Exec(
		`insert into events (rkey, nsid, event) values (?, ?, ?)`,
		event.Rkey,
		event.Nsid,
		event.EventJson,
	)

	notifier.NotifyAll()

	return err
}

func (d *DB) GetEvents(cursor string) ([]Event, error) {
	whereClause := ""
	args := []any{}
	if cursor != "" {
		whereClause = "where rkey > ?"
		args = append(args, cursor)
	}

	query := fmt.Sprintf(`
		select rkey, nsid, event
		from events
		%s
		order by rkey asc
		limit 100
	`, whereClause)

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var evts []Event
	for rows.Next() {
		var ev Event
		rows.Scan(&ev.Rkey, &ev.Nsid, &ev.EventJson)
		evts = append(evts, ev)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return evts, nil
}
