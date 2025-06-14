package db

func (d *DB) AddRepo(knot, owner, name string) error {
	_, err := d.Exec(`insert or ignore into repos (knot, owner, name) values (?, ?, ?)`, knot, owner, name)
	return err
}

func (d *DB) Knots() ([]string, error) {
	rows, err := d.Query(`select knot from repos`)
	if err != nil {
		return nil, err
	}

	var knots []string
	for rows.Next() {
		var knot string
		if err := rows.Scan(&knot); err != nil {
			return nil, err
		}
		knots = append(knots, knot)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return knots, nil
}
