package es

type Aggregate struct {
  Id       AggregateId    `json:"id"`
  Events   []DecodedEvent `json:"events,omitempty"`
  Revision Revision       `json:"revision"`
}
