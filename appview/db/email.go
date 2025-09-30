package db

import (
	"strings"
	"time"

	"tangled.org/core/appview/models"
)

func GetPrimaryEmail(e Execer, did string) (models.Email, error) {
	query := `
		select id, did, email, verified, is_primary, verification_code, last_sent, created
		from emails
		where did = ? and is_primary = true
	`
	var email models.Email
	var createdStr string
	var lastSent string
	err := e.QueryRow(query, did).Scan(&email.ID, &email.Did, &email.Address, &email.Verified, &email.Primary, &email.VerificationCode, &lastSent, &createdStr)
	if err != nil {
		return models.Email{}, err
	}
	email.CreatedAt, err = time.Parse(time.RFC3339, createdStr)
	if err != nil {
		return models.Email{}, err
	}
	parsedTime, err := time.Parse(time.RFC3339, lastSent)
	if err != nil {
		return models.Email{}, err
	}
	email.LastSent = &parsedTime
	return email, nil
}

func GetEmail(e Execer, did string, em string) (models.Email, error) {
	query := `
		select id, did, email, verified, is_primary, verification_code, last_sent, created
		from emails
		where did = ? and email = ?
	`
	var email models.Email
	var createdStr string
	var lastSent string
	err := e.QueryRow(query, did, em).Scan(&email.ID, &email.Did, &email.Address, &email.Verified, &email.Primary, &email.VerificationCode, &lastSent, &createdStr)
	if err != nil {
		return models.Email{}, err
	}
	email.CreatedAt, err = time.Parse(time.RFC3339, createdStr)
	if err != nil {
		return models.Email{}, err
	}
	parsedTime, err := time.Parse(time.RFC3339, lastSent)
	if err != nil {
		return models.Email{}, err
	}
	email.LastSent = &parsedTime
	return email, nil
}

func GetDidForEmail(e Execer, em string) (string, error) {
	query := `
		select did
		from emails
		where email = ?
	`
	var did string
	err := e.QueryRow(query, em).Scan(&did)
	if err != nil {
		return "", err
	}
	return did, nil
}

func GetEmailToDid(e Execer, emails []string, isVerifiedFilter bool) (map[string]string, error) {
	if len(emails) == 0 {
		return make(map[string]string), nil
	}

	verifiedFilter := 0
	if isVerifiedFilter {
		verifiedFilter = 1
	}

	assoc := make(map[string]string)

	// Create placeholders for the IN clause
	placeholders := make([]string, 0, len(emails))
	args := make([]any, 1, len(emails)+1)

	args[0] = verifiedFilter
	for _, email := range emails {
		if strings.HasPrefix(email, "did:") {
			assoc[email] = email
			continue
		}
		placeholders = append(placeholders, "?")
		args = append(args, email)
	}

	query := `
		select email, did
		from emails
		where
			verified = ?
			and email in (` + strings.Join(placeholders, ",") + `)
	`

	rows, err := e.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var email, did string
		if err := rows.Scan(&email, &did); err != nil {
			return nil, err
		}
		assoc[email] = did
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return assoc, nil
}

func GetVerificationCodeForEmail(e Execer, did string, email string) (string, error) {
	query := `
		select verification_code
		from emails
		where did = ? and email = ?
	`
	var code string
	err := e.QueryRow(query, did, email).Scan(&code)
	if err != nil {
		return "", err
	}
	return code, nil
}

func CheckEmailExists(e Execer, did string, email string) (bool, error) {
	query := `
		select count(*)
		from emails
		where did = ? and email = ?
	`
	var count int
	err := e.QueryRow(query, did, email).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func CheckEmailExistsAtAll(e Execer, email string) (bool, error) {
	query := `
		select count(*)
		from emails
		where email = ?
	`
	var count int
	err := e.QueryRow(query, email).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func CheckValidVerificationCode(e Execer, did string, email string, code string) (bool, error) {
	query := `
		select count(*)
		from emails
		where did = ? and email = ? and verification_code = ?
	`
	var count int
	err := e.QueryRow(query, did, email, code).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func AddEmail(e Execer, email models.Email) error {
	// Check if this is the first email for this DID
	countQuery := `
		select count(*)
		from emails
		where did = ?
	`
	var count int
	err := e.QueryRow(countQuery, email.Did).Scan(&count)
	if err != nil {
		return err
	}

	// If this is the first email, mark it as primary
	if count == 0 {
		email.Primary = true
	}

	query := `
		insert into emails (did, email, verified, is_primary, verification_code)
		values (?, ?, ?, ?, ?)
	`
	_, err = e.Exec(query, email.Did, email.Address, email.Verified, email.Primary, email.VerificationCode)
	return err
}

func DeleteEmail(e Execer, did string, email string) error {
	query := `
		delete from emails
		where did = ? and email = ?
	`
	_, err := e.Exec(query, did, email)
	return err
}

func MarkEmailVerified(e Execer, did string, email string) error {
	query := `
		update emails
		set verified = true
		where did = ? and email = ?
	`
	_, err := e.Exec(query, did, email)
	return err
}

func MakeEmailPrimary(e Execer, did string, email string) error {
	// First, unset all primary emails for this DID
	query1 := `
		update emails
		set is_primary = false
		where did = ?
	`
	_, err := e.Exec(query1, did)
	if err != nil {
		return err
	}

	// Then, set the specified email as primary
	query2 := `
		update emails
		set is_primary = true
		where did = ? and email = ?
	`
	_, err = e.Exec(query2, did, email)
	return err
}

func GetAllEmails(e Execer, did string) ([]models.Email, error) {
	query := `
		select did, email, verified, is_primary, verification_code, last_sent, created
		from emails
		where did = ?
	`
	rows, err := e.Query(query, did)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var emails []models.Email
	for rows.Next() {
		var email models.Email
		var createdStr string
		var lastSent string
		err := rows.Scan(&email.Did, &email.Address, &email.Verified, &email.Primary, &email.VerificationCode, &lastSent, &createdStr)
		if err != nil {
			return nil, err
		}
		email.CreatedAt, err = time.Parse(time.RFC3339, createdStr)
		if err != nil {
			return nil, err
		}
		parsedTime, err := time.Parse(time.RFC3339, lastSent)
		if err != nil {
			return nil, err
		}
		email.LastSent = &parsedTime
		emails = append(emails, email)
	}
	return emails, nil
}

func UpdateVerificationCode(e Execer, did string, email string, code string) error {
	query := `
		update emails
		set verification_code = ?,
			last_sent = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		where did = ? and email = ?
	`
	_, err := e.Exec(query, code, did, email)
	return err
}
