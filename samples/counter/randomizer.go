package counter

import (
	"math/rand"
	"time"
)

type Randomizer = func() int

func PseudoRandomizer() Randomizer {
	rand.Seed(time.Now().UnixNano())
	return func() int {
		return rand.Intn(1000)
	}
}
