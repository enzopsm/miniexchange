package domain

import "time"

// Webhook represents a broker's subscription to an event notification.
type Webhook struct {
	WebhookID string
	BrokerID  string
	Event     string
	URL       string
	CreatedAt time.Time
	UpdatedAt time.Time
}
