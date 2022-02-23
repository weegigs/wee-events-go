package counter

import (
	"math/rand"
	"time"

	"github.com/google/wire"

	"github.com/weegigs/wee-events-go/dynamo"
)

func PseudoRandomizer() Randomizer {
	rand.Seed(time.Now().UnixNano())
	return func() int {
		return rand.Intn(1000)
	}
}

type controller struct{}

var Controller = controller{}

var Live = wire.NewSet(PseudoRandomizer,
	CreateCounterService,
	dynamo.Live)

var Local = wire.NewSet(PseudoRandomizer,
	CreateCounterService,
	dynamo.Local)

// func LocalCounterService(ctx context.Context, tableName dynamo.EventsTableName, randomize counter.Randomizer) (we.EntityService[counter.Counter], error) {
// 	panic(wire.Build(
// 		support.AWSConfig,
// 		counter.CreateCounterService,
// 		dynamo.EventStore,
// 	))
// }
