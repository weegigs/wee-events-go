package internal

import (
	"encoding/binary"
	"github.com/oklog/ulid/v2"
	"github.com/weegigs/wee-events-go/we"
)

func EncodeRevision(timestamp uint64, sequence uint64, index uint16) (we.Revision, error) {
	r := &ulid.ULID{}
	err := r.SetTime(timestamp)

	if err != nil {
		return "", err
	}

	entropy := make([]byte, 10)
	binary.BigEndian.PutUint64(entropy[:8], sequence)
	binary.BigEndian.PutUint16(entropy[8:], index)

	err = r.SetEntropy(entropy)
	if err != nil {
		return "", err
	}

	return we.Revision(r.String()), nil
}

func DecodeSequenceNumber(revision we.Revision) (uint64, error) {
	parsed, err := ulid.Parse(revision.String())
	if err != nil {
		return 0, err
	}

	sequence := binary.BigEndian.Uint64(parsed.Entropy()[:8])

	return sequence, nil
}
