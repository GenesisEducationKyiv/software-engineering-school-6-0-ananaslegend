//go:build integration

package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Mailpit wraps a testcontainer running axllent/mailpit. SMTP is reachable via
// SMTPHost:SMTPPort; the REST API used to inspect delivered mail is at APIBase.
//
// Lifetime is bound to the test that called NewMailpit via t.Cleanup.
type Mailpit struct {
	SMTPHost string
	SMTPPort int
	APIBase  string

	container testcontainers.Container
	client    *http.Client
}

// MailpitMessage is the trimmed subset of the Mailpit message-summary record
// that integration tests actually rely on.
type MailpitMessage struct {
	ID      string          `json:"ID"`
	From    MailpitAddress  `json:"From"`
	To      []MailpitAddress `json:"To"`
	Subject string          `json:"Subject"`
	Snippet string          `json:"Snippet"`
	Created time.Time       `json:"Created"`
}

// MailpitAddress mirrors the {Name, Address} shape Mailpit returns.
type MailpitAddress struct {
	Name    string `json:"Name"`
	Address string `json:"Address"`
}

// NewMailpit starts an axllent/mailpit:latest container exposing SMTP (1025/tcp)
// and the web/API (8025/tcp), waits for the API to come up, and returns a ready
// helper. The container is terminated automatically when the calling test ends.
func NewMailpit(ctx context.Context, t testing.TB) *Mailpit {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "axllent/mailpit:latest",
		ExposedPorts: []string{"1025/tcp", "8025/tcp"},
		WaitingFor: wait.ForHTTP("/api/v1/info").
			WithPort("8025/tcp").
			WithStartupTimeout(30 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "start mailpit container")

	smtpHost, err := container.Host(ctx)
	require.NoError(t, err, "mailpit host")
	smtpMapped, err := container.MappedPort(ctx, "1025/tcp")
	require.NoError(t, err, "mailpit smtp mapped port")
	apiMapped, err := container.MappedPort(ctx, "8025/tcp")
	require.NoError(t, err, "mailpit api mapped port")

	m := &Mailpit{
		SMTPHost:  smtpHost,
		SMTPPort:  int(smtpMapped.Num()),
		APIBase:   "http://" + smtpHost + ":" + strconv.Itoa(int(apiMapped.Num())),
		container: container,
		client:    &http.Client{Timeout: 5 * time.Second},
	}

	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})
	return m
}

// Reset empties Mailpit's mailbox so the calling test starts from a known state.
func (m *Mailpit) Reset(ctx context.Context, t testing.TB) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, m.APIBase+"/api/v1/messages", nil)
	require.NoError(t, err)
	resp, err := m.client.Do(req)
	require.NoError(t, err, "mailpit delete messages")
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "mailpit delete messages status")
}

// Messages returns the current message-summary list, newest-first per Mailpit
// convention. Callers that need a chronological order should reverse the slice.
func (m *Mailpit) Messages(ctx context.Context, t testing.TB) []MailpitMessage {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.APIBase+"/api/v1/messages", nil)
	require.NoError(t, err)
	resp, err := m.client.Do(req)
	require.NoError(t, err, "mailpit list messages")
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "mailpit list messages status")

	var payload struct {
		Messages []MailpitMessage `json:"messages"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	return payload.Messages
}

// WaitForMessages polls Mailpit until at least `count` messages are visible
// or the deadline elapses. DialAndSend can return before mailpit indexes the
// message; this short loop bridges that gap deterministically.
func (m *Mailpit) WaitForMessages(ctx context.Context, t testing.TB, count int, within time.Duration) []MailpitMessage {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		msgs := m.Messages(ctx, t)
		if len(msgs) >= count {
			return msgs
		}
		if time.Now().After(deadline) {
			require.Failf(t, "mailpit: timed out waiting for messages",
				"want >= %d, got %d after %s", count, len(msgs), within)
			return msgs
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// MessageSource returns the raw RFC-822 source of one message so callers can
// assert on body content (e.g. the confirm URL embedded in the HTML/text parts).
func (m *Mailpit) MessageSource(ctx context.Context, t testing.TB, id string) string {
	t.Helper()
	url := fmt.Sprintf("%s/api/v1/message/%s/raw", m.APIBase, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := m.client.Do(req)
	require.NoError(t, err, "mailpit message source")
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "mailpit message source status")
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}
