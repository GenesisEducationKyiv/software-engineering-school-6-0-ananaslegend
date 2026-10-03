// Package emailermetrics wraps an email.Sender with RED instrumentation
// (rate / errors / duration) labelled by driver.
package emailermetrics

import (
	"context"
	"time"

	"github.com/ananaslegend/reposeetory/internal/notifications/contract"
	"github.com/ananaslegend/reposeetory/internal/notifications/email"
	"github.com/ananaslegend/reposeetory/internal/observability/redmetrics"
)

type instrumented struct {
	inner  email.Sender
	red    *redmetrics.RED
	driver string
}

// Wrap returns inner annotated with RED metrics on every SendRelease / SendConfirmation call.
// driver is recorded as a label value ("resend" | "smtp" | "stub").
func Wrap(inner email.Sender, driver string, red *redmetrics.RED) email.Sender {
	return &instrumented{inner: inner, red: red, driver: driver}
}

func (w *instrumented) SendRelease(ctx context.Context, p contract.SendReleaseRequest) error {
	start := time.Now()
	err := w.inner.SendRelease(ctx, p)
	w.red.Observe(resultOf(err), time.Since(start), w.driver)
	return err //nolint:wrapcheck // transparent instrumentation wrapper; inner mailer already wraps with its own pkg.Struct.Method prefix
}

func (w *instrumented) SendConfirmation(ctx context.Context, p contract.SendConfirmationRequest) error {
	start := time.Now()
	err := w.inner.SendConfirmation(ctx, p)
	w.red.Observe(resultOf(err), time.Since(start), w.driver)
	return err //nolint:wrapcheck // transparent instrumentation wrapper; inner mailer already wraps with its own pkg.Struct.Method prefix
}

func resultOf(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}
