package we

import (
	"github.com/goccy/go-json"
)

func NewJsonEventMarshaller() JsonEventMarshaller {
	return JsonEventMarshaller{}
}

type JsonEventMarshaller struct{}

func (JsonEventMarshaller) Marshal(event any) (Data, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return Data{}, err
	}

	return Data{
		Encoding: "application/json",
		Data:     data,
	}, nil
}

func (JsonEventMarshaller) Unmarshal(data Data, value any) error {
	if data.Encoding != "application/json" {
		return InvalidEncoding("application/json", data.Encoding)
	}
	return json.Unmarshal(data.Data, value)
}
