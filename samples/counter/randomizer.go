package counter

import (
	"math/rand"
	"time"
)

type Randomizer = func() int

func PseudoRandomizer() Randomizer {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return func() int {
		return r.Intn(1000)
	}
}
