package we

type Aggregate struct {
	Id       AggregateId     `json:"id"`
	Events   []RecordedEvent `json:"events,omitempty"`
	Revision Revision        `json:"revision"`
}
