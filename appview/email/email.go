package email

import (
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/resend/resend-go/v2"
)

type Email struct {
	From    string
	To      string
	Subject string
	Text    string
	Html    string
	APIKey  string
}

func SendEmail(email Email) error {
	client := resend.NewClient(email.APIKey)
	_, err := client.Emails.Send(&resend.SendEmailRequest{
		From:    email.From,
		To:      []string{email.To},
		Subject: email.Subject,
		Text:    email.Text,
		Html:    email.Html,
	})
	if err != nil {
		return fmt.Errorf("error sending email: %w", err)
	}
	return nil
}

func IsValidEmail(email string) bool {
	// Basic length check
	if len(email) < 3 || len(email) > 254 {
		return false
	}

	// Regular expression for email validation (RFC 5322 compliant)
	pattern := `^[a-zA-Z0-9.!#$%&'*+/=?^_\x60{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`

	// Compile regex
	regex := regexp.MustCompile(pattern)

	// Check if email matches regex pattern
	if !regex.MatchString(email) {
		return false
	}

	// Split email into local and domain parts
	parts := strings.Split(email, "@")
	domain := parts[1]

	mx, err := net.LookupMX(domain)
	if err != nil || len(mx) == 0 {
		return false
	}

	return true
}
