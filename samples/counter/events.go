package counter

import es "github.com/weegigs/wee-events-go"

// var IncrementedEvent = es.EventType("counter:increment")

type Incremented struct {
	Amount int `json:"amount"`
}

// func (Incremented) EventType() es.EventType {
// 	return IncrementedEvent
// }

// var DecrementedEvent = es.EventType("counter:decrement")

type Decremented struct {
	Amount int `json:"amount"`
}

// func (Decremented) EventType() es.EventType {
// 	return DecrementedEvent
// }

var RandomizedEvent = es.EventType("counter:randomized")

type Randomized struct {
	Value int `json:"value"`
}

func (Randomized) EventType() es.EventType {
	return RandomizedEvent
}

// func CounterMarshaller() es.EventMarshaller {
// 	return es.JsonEventMarshaller(
// 		func(event *es.RecordedEvent) (any, error) {
// 			switch event.EventType {
// 			case IncrementedEvent:
// 				var v Incremented
// 				if err := json.Unmarshal(event.Data, &v); err != nil {
// 					return nil, err
// 				}

// 				return v, nil

// 			case DecrementedEvent:
// 				var v Decremented
// 				if err := json.Unmarshal(event.Data, &v); err != nil {
// 					return nil, err
// 				}

// 				return v, nil

// 			case RandomizedEvent:
// 				var v Randomized
// 				if err := json.Unmarshal(event.Data, &v); err != nil {
// 					return nil, err
// 				}

// 				return v, nil
// 			}

// 			return nil, nil
// 		},
// 	)
// }
