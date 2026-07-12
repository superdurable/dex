package seq

import "encoding/json"

type StringSequence []string

// UnmarshalJSON implements json.Unmarshaler to handle both string and []string
func (s *StringSequence) UnmarshalJSON(data []byte) error {
	var slice []string
	if err := json.Unmarshal(data, &slice); err == nil {
		*s = slice
		return nil
	}

	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		*s = []string{str}
		return nil
	}

	return json.Unmarshal(data, &slice)
}
