package es

import (
  "errors"

  "github.com/mitchellh/mapstructure"
)

type Decode = func(map[string]interface{}, interface{}) error

type Decoder struct {
  decoders map[EventType]Decode
}

func NewDecoder() *Decoder {
  return &Decoder{
    decoders: make(map[EventType]Decode, 13),
  }
}

func (d *Decoder) AddDecoder(eventType EventType, decode Decode) {
  d.decoders[eventType] = decode
}

func (d *Decoder) Decode(input map[string]interface{}, output interface{}) error {
  eventType, ok := input["type"].(EventType)
  if !ok {
    return errors.New("unable to determine event type")
  }

  decoder := d.decoders[eventType]
  if decoder == nil {
    return defaultDecoder(input, output)
  }

  return decoder(input, output)
}


func defaultDecoder(input map[string]interface{}, output interface{}) error {
  config := &mapstructure.DecoderConfig{
    ErrorUnused: true,
    Result:      output,
  }

  decoder, err := mapstructure.NewDecoder(config)
  if err != nil {
    return err
  }

  return decoder.Decode(input)
}
