package jetstream

import (
  "context"
  "fmt"
  "github.com/nats-io/nats.go"
  "github.com/testcontainers/testcontainers-go"
  "github.com/testcontainers/testcontainers-go/wait"
)

func NewTestStore(ctx context.Context, options ...EventStoreOption) (*EventStore, func(), error) {
  db, err := testcontainers.GenericContainer(
    ctx, testcontainers.GenericContainerRequest{
      ContainerRequest: testcontainers.ContainerRequest{
        Image:        "nats:alpine",
        ExposedPorts: []string{"4222/tcp"},
        WaitingFor:   wait.ForListeningPort("4222"),
        Cmd:          []string{"--jetstream"},
      },
      Started: true,
    },
  )
  if err != nil {
    return nil, nil, err
  }

  host, err := db.Host(ctx)
  if err != nil {
    return nil, nil, err
  }

  port, err := db.MappedPort(ctx, "4222")
  if err != nil {
    return nil, nil, err
  }

  url := fmt.Sprintf("nats://%s:%s", host, port.Port())
  nc, err := nats.Connect(url)
  if err != nil {
    return nil, nil, err
  }

  store := NewEventStore("test", nc, options...)

  return store, func() {
    if err := db.Terminate(ctx); err != nil {
      panic(err)
    }
  }, nil
}
