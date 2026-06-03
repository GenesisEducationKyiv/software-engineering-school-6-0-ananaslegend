package emailermetrics_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/ananaslegend/reposeetory/internal/notifier/emailer"
	"github.com/ananaslegend/reposeetory/internal/observability/emailermetrics"
	"github.com/ananaslegend/reposeetory/internal/observability/redmetrics"
	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

type fakeMailer struct {
	releaseErr      error
	confirmationErr error
}

func (f *fakeMailer) SendRelease(_ context.Context, _ domain.SendReleaseParams) error {
	return f.releaseErr
}
func (f *fakeMailer) SendConfirmation(_ context.Context, _ domain.SendConfirmationParams) error {
	return f.confirmationErr
}

func TestWrap_RecordsOkAndErrorByDriver(t *testing.T) {
	reg := prometheus.NewRegistry()
	red := redmetrics.New(redmetrics.Config{
		Subsystem: "email", ExtraLabels: []string{"driver"}, Registry: reg,
	})

	okMailer := emailermetrics.Wrap(&fakeMailer{}, "resend", red)
	failMailer := emailermetrics.Wrap(&fakeMailer{releaseErr: errors.New("boom")}, "smtp", red)

	_ = okMailer.SendRelease(context.Background(), domain.SendReleaseParams{})
	_ = failMailer.SendRelease(context.Background(), domain.SendReleaseParams{})

	const expected = `
# HELP email_requests_total Total number of email operations.
# TYPE email_requests_total counter
email_requests_total{driver="resend",result="ok"} 1
email_requests_total{driver="smtp",result="error"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "email_requests_total"); err != nil {
		t.Fatal(err)
	}
}

var _ emailer.Emailer = (*fakeMailer)(nil)
