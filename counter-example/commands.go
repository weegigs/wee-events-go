package counter_example

import (
  es "github.com/weegigs/wee-events-go"
)

const IncrementCmd = "counter:increment"

type Increment struct {
  Amount int
}

func (Increment) Type() es.CommandType {
  return IncrementCmd
}

const DecrementCmd = "counter:decrement"

type Decrement struct {
  Amount int
}

func (Decrement) Type() es.CommandType {
  return DecrementCmd
}

const RandomizeCmd = "counter:randomize"

type Randomize struct {
  Amount int
}

func (Randomize) Type() es.CommandType {
  return RandomizeCmd
}
