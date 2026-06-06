package account

// Opened is the genesis event that brings an account into existence.
type Opened struct {
	Owner string `json:"owner"`
}

type Deposited struct {
	Amount int `json:"amount"`
}

type Withdrawn struct {
	Amount int `json:"amount"`
}
