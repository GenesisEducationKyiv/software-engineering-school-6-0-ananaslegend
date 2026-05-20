package confirmer

import "fmt"

// ConfirmURL returns the canonical subscription-confirmation link.
func ConfirmURL(baseURL, token string) string {
	return fmt.Sprintf("%s/api/confirm/%s", baseURL, token)
}
