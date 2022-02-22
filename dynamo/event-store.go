package dynamo

import (
	"github.com/google/wire"
	we "github.com/weegigs/wee-events-go"
)

var EventStore = wire.NewSet(
	Client,
	NewEventStore,
	we.NewJsonEventEncoder,
	wire.Bind(new(we.EventEncoder), new(*we.JsonEventEncoder)),
	wire.Bind(new(we.EventStore), new(*DynamoEventStore)),
)
