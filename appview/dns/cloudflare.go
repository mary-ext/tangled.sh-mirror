package dns

import (
	"context"
	"fmt"

	"github.com/cloudflare/cloudflare-go"
	"tangled.sh/tangled.sh/core/appview/config"
)

type Record struct {
	Type    string
	Name    string
	Content string
	TTL     int
	Proxied bool
}

type Cloudflare struct {
	api  *cloudflare.API
	zone string
}

func NewCloudflare(c *config.Config) (*Cloudflare, error) {
	apiToken := c.Cloudflare.ApiToken
	api, err := cloudflare.NewWithAPIToken(apiToken)
	if err != nil {
		return nil, err
	}
	return &Cloudflare{api: api, zone: c.Cloudflare.ZoneId}, nil
}

func (cf *Cloudflare) CreateDNSRecord(ctx context.Context, record Record) error {
	_, err := cf.api.CreateDNSRecord(ctx, cloudflare.ZoneIdentifier(cf.zone), cloudflare.CreateDNSRecordParams{
		Type:    record.Type,
		Name:    record.Name,
		Content: record.Content,
		TTL:     record.TTL,
		Proxied: &record.Proxied,
	})
	if err != nil {
		return fmt.Errorf("failed to create DNS record: %w", err)
	}
	return nil
}

func (cf *Cloudflare) DeleteDNSRecord(ctx context.Context, recordID string) error {
	err := cf.api.DeleteDNSRecord(ctx, cloudflare.ZoneIdentifier(cf.zone), recordID)
	if err != nil {
		return fmt.Errorf("failed to delete DNS record: %w", err)
	}
	return nil
}
