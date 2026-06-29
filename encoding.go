package events

import "encoding/json"

// marshalTyped serialises a typed value to bytes for storage in the event log.
// Centralised here so the encoding strategy (JSON, msgpack, protobuf…)
// can be changed in one place without touching bus or compactor logic.
func marshalTyped[T any](v T) ([]byte, error) {
	return json.Marshal(v)
}

// unmarshalTyped deserialises bytes from the event log into a typed value.
func unmarshalTyped[T any](data []byte) (T, error) {
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return v, err
	}
	return v, nil
}
