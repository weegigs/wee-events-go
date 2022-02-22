package main

import (
	"context"
	"log"
	"math/rand"
	"net/http"

	we "github.com/weegigs/wee-events-go"
	"github.com/weegigs/wee-events-go/dynamo"
)

func r() int {
	value := rand.Int()
	if value < 0 {
		return -value
	}

	return value
}

func run() error {
	service, err := DynamoCounterService(context.Background(), dynamo.EventsTableName("events"), r)
	if err != nil {
		log.Fatalf("failed to configure controller: %v", err)
	}

	handler := we.HttpHandler(service)

	log.Println("listening on :9080")
	return http.ListenAndServe(":9080", handler)
}

func main() {
	if err := run(); err != nil {
		log.Fatal("ListenAndServe:", err)
	}
}
