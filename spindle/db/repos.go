package db

type Repo struct {
	Knot  string
	Owner string
	Name  string
}

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

func (d *DB) GetRepo(knot, owner, name string) (*Repo, error) {
	var repo Repo

	query := "select knot, owner, name from repos where knot = ? and owner = ? and name = ?"
	err := d.DB.QueryRow(query, knot, owner, name).
		Scan(&repo.Knot, &repo.Owner, &repo.Name)

	if err != nil {
		return nil, err
	}

	return &repo, nil
}
