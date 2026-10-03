package notifier

import "fmt"

// UnsubscribeURL returns the canonical unsubscribe link for a subscription.
func UnsubscribeURL(baseURL, token string) string {
	return fmt.Sprintf("%s/api/unsubscribe/%s", baseURL, token)
}
