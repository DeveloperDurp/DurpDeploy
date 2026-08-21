package agentproto

import (
	"bytes"
	"encoding/json"
)

func hasDuplicateJSONMember(payload []byte) (bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	return scanJSONValue(decoder)
}

func scanJSONValue(decoder *json.Decoder) (bool, error) {
	token, err := decoder.Token()
	if err != nil {
		return false, err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return false, nil
	}
	switch delimiter {
	case '{':
		return scanJSONObject(decoder)
	case '[':
		return scanJSONArray(decoder)
	default:
		return false, nil
	}
}

func scanJSONObject(decoder *json.Decoder) (bool, error) {
	members := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return false, err
		}
		member, ok := token.(string)
		if !ok {
			return false, ErrInvalidJSON
		}
		if _, exists := members[member]; exists {
			return true, nil
		}
		members[member] = struct{}{}
		duplicate, err := scanJSONValue(decoder)
		if err != nil || duplicate {
			return duplicate, err
		}
	}
	_, err := decoder.Token()
	return false, err
}

func scanJSONArray(decoder *json.Decoder) (bool, error) {
	for decoder.More() {
		duplicate, err := scanJSONValue(decoder)
		if err != nil || duplicate {
			return duplicate, err
		}
	}
	_, err := decoder.Token()
	return false, err
}
