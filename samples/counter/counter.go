package counter

type Counter struct {
	Current int `json:"current"`
}

func (state *Counter) Value() int {
	return state.Current
}
