package counter_example

import (
  "context"
  "math/rand"
  "testing"
  "time"

  "github.com/oklog/ulid/v2"
  "github.com/stretchr/testify/assert"
  es "github.com/weegigs/wee-events-go"
)

var entropy = ulid.Monotonic(rand.New(rand.NewSource(time.Now().UnixNano())), 0)

func createId() es.AggregateId {
  return es.AggregateId{
    Type: "go-test",
    Key:  ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String(),
  }
}

type test = func(t *testing.T)

func loadInitialCounter(controller es.Controller[Counter]) test {
  return func(t *testing.T) {
    // ctx, cancel := context.WithTimeout(context.Background(), time.Duration(5)*time.Second)
    // defer cancel()
    ctx := context.TODO()

    entity, err := controller.Load(
      ctx, es.AggregateId{
        Type: "counter",
        Key:  "test-1",
      },
    )

    if err != nil {
      t.Fatalf("Unexpected failure %+v", err)
    }

    assert.Equal(t, es.InitialRevision, entity.Revision)
    assert.Equal(t, false, entity.Initialised())
  }
}

func incrementsCounter(controller es.Controller[Counter]) test {
  return func(t *testing.T) {
    // ctx, cancel := context.WithTimeout(context.Background(), time.Duration(5)*time.Second)
    // defer cancel()
    ctx := context.TODO()

    entity, err := controller.Execute(
      ctx, es.AggregateId{
        Type: "counter",
        Key:  "test-2",
      },
      Increment{
        Amount: 7,
      },
    )

    if err != nil {
      t.Fatalf("Unexpected failure %+v", err)
    }

    assert.NotEqual(t, es.InitialRevision, entity.Revision)
    assert.Equal(t, true, entity.Initialised())
    assert.Equal(t, 7, entity.State.Value())
  }
}

func decrementsCounter(controller es.Controller[Counter]) test {
  return func(t *testing.T) {
    // ctx, cancel := context.WithTimeout(context.Background(), time.Duration(5)*time.Second)
    // defer cancel()
    ctx := context.TODO()

    _, err := controller.Execute(
      ctx, es.AggregateId{
        Type: "counter",
        Key:  "test-3",
      },
      Increment{
        Amount: 7,
      },
    )

    if err != nil {
      t.Fatalf("Unexpected failure %+v", err)
    }

    entity, err := controller.Execute(
      ctx, es.AggregateId{
        Type: "counter",
        Key:  "test-2",
      },
      Decrement{
        Amount: 5,
      },
    )

    if err != nil {
      t.Fatalf("Unexpected failure %+v", err)
    }

    assert.NotEqual(t, es.InitialRevision, entity.Revision)
    assert.Equal(t, true, entity.Initialised())
    assert.Equal(t, 2, entity.State.Value())
  }
}

func TestCounterController(t *testing.T) {
  ts, err := es.DynamoTestStore(context.Background(), CounterMarshaller())
  if err != nil {
    t.Fatalf("failed to create test store. %+v", err)
  }

  defer ts.TearDown()
  controller := CounterController()(ts.EventStore)

  t.Run("load initial entity", loadInitialCounter(controller))
  t.Run("increment counter", incrementsCounter(controller))
  t.Run("decrement counter", decrementsCounter(controller))
}
