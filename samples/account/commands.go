package account

type Open struct {
	Owner string
}

type Deposit struct {
	Amount int
}

type Withdraw struct {
	Amount int
}
