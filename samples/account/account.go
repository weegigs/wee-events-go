package account

// Account is a genesis-event aggregate: its Owner can only come from an Opened
// event, so the zero value is not a valid account. Contrast with the counter
// sample, whose zero value (0) is a valid starting state.
type Account struct {
	Owner   string `json:"owner"`
	Balance int    `json:"balance"`
}
