package db

import (
	"tangled.org/core/appview/models"
)

func AddInflightSignup(e Execer, signup models.InflightSignup) error {
	query := `insert into signups_inflight (email, invite_code) values (?, ?)`
	_, err := e.Exec(query, signup.Email, signup.InviteCode)
	return err
}

func DeleteInflightSignup(e Execer, email string) error {
	query := `delete from signups_inflight where email = ?`
	_, err := e.Exec(query, email)
	return err
}

func GetEmailForCode(e Execer, inviteCode string) (string, error) {
	query := `select email from signups_inflight where invite_code = ?`
	var email string
	err := e.QueryRow(query, inviteCode).Scan(&email)
	return email, err
}
