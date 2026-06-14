// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package email

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/aws/aws-sdk-go-v2/service/ses/types"
)

// SESProvider sends email via AWS SES.
type SESProvider struct {
	client *ses.Client
	from   string
}

// NewSESProvider constructs an SESProvider from an AWS config and a verified
// sender address.
func NewSESProvider(cfg aws.Config, fromAddress string) *SESProvider {
	return &SESProvider{
		client: ses.NewFromConfig(cfg),
		from:   fromAddress,
	}
}

func (s *SESProvider) Send(ctx context.Context, msg Message) error {
	input := &ses.SendEmailInput{
		Source: aws.String(s.from),
		Destination: &types.Destination{
			ToAddresses: []string{msg.To},
		},
		Message: &types.Message{
			Subject: &types.Content{Data: aws.String(msg.Subject)},
			Body: &types.Body{
				Html: &types.Content{Data: aws.String(msg.HTMLBody)},
				Text: &types.Content{Data: aws.String(msg.TextBody)},
			},
		},
	}
	if _, err := s.client.SendEmail(ctx, input); err != nil {
		return fmt.Errorf("ses send email to %s: %w", msg.To, err)
	}
	return nil
}
