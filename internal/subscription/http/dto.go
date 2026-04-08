package http

type subscribeRequest struct {
	Email      string `json:"email"`
	Repository string `json:"repository"`
}

type statusResponse struct {
	Status string `json:"status"`
}
