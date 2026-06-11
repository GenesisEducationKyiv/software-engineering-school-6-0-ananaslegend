package transport_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/ananaslegend/reposeetory/internal/notifications/contract"
	"github.com/ananaslegend/reposeetory/internal/notifications/transport"
	"github.com/ananaslegend/reposeetory/internal/notifications/transport/mocks"
)

func newHandler(t *testing.T) (*transport.Handler, *mocks.MockSender, *prometheus.Registry) {
	ctrl := gomock.NewController(t)
	em := mocks.NewMockSender(ctrl)
	reg := prometheus.NewRegistry()
	return transport.NewHandler(transport.Config{Sender: em, Registry: reg}), em, reg
}

func TestSendRelease_OK(t *testing.T) {
	h, em, reg := newHandler(t)
	em.EXPECT().SendRelease(gomock.Any(), contract.SendReleaseRequest{
		To: "u@example.com", RepoFullName: "owner/name", ReleaseTag: "v1",
		ReleaseURL: "https://r", UnsubscribeURL: "https://u",
	}).Return(nil)

	body := `{"to":"u@example.com","repo_full_name":"owner/name","release_tag":"v1","release_url":"https://r","unsubscribe_url":"https://u"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/notifications/release", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.SendRelease(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP notifications_sent_total Total notifications attempted by the notifications service.
# TYPE notifications_sent_total counter
notifications_sent_total{result="ok",type="release"} 1
`), "notifications_sent_total"))
}

func TestSendRelease_MalformedJSON_400(t *testing.T) {
	h, _, _ := newHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/notifications/release", strings.NewReader("{not json"))
	rec := httptest.NewRecorder()
	h.SendRelease(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSendRelease_MissingTo_400(t *testing.T) {
	h, _, _ := newHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/notifications/release", strings.NewReader(`{"release_tag":"v1"}`))
	rec := httptest.NewRecorder()
	h.SendRelease(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSendRelease_ProviderError_502(t *testing.T) {
	h, em, reg := newHandler(t)
	em.EXPECT().SendRelease(gomock.Any(), gomock.Any()).Return(assertErr())

	body := `{"to":"u@example.com","repo_full_name":"o/n","release_tag":"v1","release_url":"r","unsubscribe_url":"u"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/notifications/release", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.SendRelease(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP notifications_sent_total Total notifications attempted by the notifications service.
# TYPE notifications_sent_total counter
notifications_sent_total{result="error",type="release"} 1
`), "notifications_sent_total"))
}

func TestSendConfirmation_OK(t *testing.T) {
	h, em, _ := newHandler(t)
	em.EXPECT().SendConfirmation(gomock.Any(), contract.SendConfirmationRequest{
		To: "u@example.com", ConfirmURL: "https://c", RepoFullName: "owner/name",
	}).Return(nil)

	body := `{"to":"u@example.com","confirm_url":"https://c","repo_full_name":"owner/name"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/notifications/confirmation", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.SendConfirmation(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func assertErr() error { return errString("provider down") }

type errString string

func (e errString) Error() string { return string(e) }
