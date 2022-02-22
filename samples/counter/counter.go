package counter

type Counter struct {
	Current int
}

func (state *Counter) Value() int {
	return state.Current
}
