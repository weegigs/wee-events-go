package ds

import (
	"context"
	"testing"

	"github.com/weegigs/wee-events-go/we"
)

// BenchmarkDynamo wires the shared benchmark suite to a disposable
// dynamodb-local testcontainer. Requires Docker.
func BenchmarkDynamo(b *testing.B) {
	ctx := context.Background()

	store, cleanup, err := DynamoTestStore(ctx)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(cleanup)

	we.NewEventStoreBenchmarkSuite(ctx, store).Run(b)
}
