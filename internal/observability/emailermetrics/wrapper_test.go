package emailermetrics_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/ananaslegend/reposeetory/internal/notifications/contract"
	"github.com/ananaslegend/reposeetory/internal/notifications/email"
	"github.com/ananaslegend/reposeetory/internal/observability/emailermetrics"
	"github.com/ananaslegend/reposeetory/internal/observability/redmetrics"
)

type fakeMailer struct {
	releaseErr      error
	confirmationErr error
}

func (f *fakeMailer) SendRelease(_ context.Context, _ contract.SendReleaseRequest) error {
	return f.releaseErr
}
func (f *fakeMailer) SendConfirmation(_ context.Context, _ contract.SendConfirmationRequest) error {
	return f.confirmationErr
}

func TestWrap_RecordsOkAndErrorByDriver(t *testing.T) {
	reg := prometheus.NewRegistry()
	red := redmetrics.New(redmetrics.Config{
		Subsystem: "email", ExtraLabels: []string{"driver"}, Registry: reg,
	})

	okMailer := emailermetrics.Wrap(&fakeMailer{}, "resend", red)
	failMailer := emailermetrics.Wrap(&fakeMailer{releaseErr: errors.New("boom")}, "smtp", red)

	_ = okMailer.SendRelease(context.Background(), contract.SendReleaseRequest{})
	_ = failMailer.SendRelease(context.Background(), contract.SendReleaseRequest{})

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

var _ email.Sender = (*fakeMailer)(nil)
