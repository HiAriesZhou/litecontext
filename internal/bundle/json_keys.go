package bundle

import (
	"fmt"
	"strconv"
	"unicode/utf8"
)

func rejectDuplicateKeys(data []byte) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("invalid JSON")
	}
	index, err := parseJSONValue(data, skipJSONSpace(data, 0))
	if err != nil {
		return err
	}
	if skipJSONSpace(data, index) != len(data) {
		return fmt.Errorf("invalid JSON")
	}
	return nil
}

func parseJSONValue(data []byte, index int) (int, error) {
	if index >= len(data) {
		return 0, fmt.Errorf("invalid JSON")
	}
	switch data[index] {
	case '{':
		return parseJSONObject(data, index)
	case '[':
		return parseJSONArray(data, index)
	case '"':
		return skipJSONString(data, index)
	case 't':
		return skipJSONLiteral(data, index, "true")
	case 'f':
		return skipJSONLiteral(data, index, "false")
	case 'n':
		return skipJSONLiteral(data, index, "null")
	default:
		return skipJSONNumber(data, index)
	}
}

func parseJSONObject(data []byte, index int) (int, error) {
	index++
	index = skipJSONSpace(data, index)
	if index < len(data) && data[index] == '}' {
		return index + 1, nil
	}
	seen := map[string]struct{}{}
	for {
		if index >= len(data) || data[index] != '"' {
			return 0, fmt.Errorf("invalid JSON")
		}
		key, next, err := parseJSONKey(data, index)
		if err != nil {
			return 0, err
		}
		if _, ok := seen[key]; ok {
			return 0, fmt.Errorf("duplicate JSON key")
		}
		seen[key] = struct{}{}
		index = skipJSONSpace(data, next)
		if index >= len(data) || data[index] != ':' {
			return 0, fmt.Errorf("invalid JSON")
		}
		index = skipJSONSpace(data, index+1)
		if key == "visibility" || key == "confidence" {
			if index >= len(data) || data[index] != '"' {
				return 0, fmt.Errorf("invalid JSON")
			}
			value, after, err := parseJSONKey(data, index)
			if err != nil {
				return 0, err
			}
			if value == "" {
				return 0, fmt.Errorf("invalid JSON")
			}
			index = after
		} else {
			var err error
			index, err = parseJSONValue(data, index)
			if err != nil {
				return 0, err
			}
		}
		index = skipJSONSpace(data, index)
		if index >= len(data) {
			return 0, fmt.Errorf("invalid JSON")
		}
		switch data[index] {
		case '}':
			return index + 1, nil
		case ',':
			index = skipJSONSpace(data, index+1)
		default:
			return 0, fmt.Errorf("invalid JSON")
		}
	}
}

func parseJSONArray(data []byte, index int) (int, error) {
	index++
	index = skipJSONSpace(data, index)
	if index < len(data) && data[index] == ']' {
		return index + 1, nil
	}
	for {
		var err error
		index, err = parseJSONValue(data, index)
		if err != nil {
			return 0, err
		}
		index = skipJSONSpace(data, index)
		if index >= len(data) {
			return 0, fmt.Errorf("invalid JSON")
		}
		switch data[index] {
		case ']':
			return index + 1, nil
		case ',':
			index = skipJSONSpace(data, index+1)
		default:
			return 0, fmt.Errorf("invalid JSON")
		}
	}
}

func parseJSONKey(data []byte, index int) (string, int, error) {
	end, err := skipJSONString(data, index)
	if err != nil {
		return "", 0, err
	}
	key, err := strconv.Unquote(string(data[index:end]))
	if err != nil {
		return "", 0, fmt.Errorf("invalid JSON")
	}
	return key, end, nil
}

func skipJSONString(data []byte, index int) (int, error) {
	if index >= len(data) || data[index] != '"' {
		return 0, fmt.Errorf("invalid JSON")
	}
	index++
	for index < len(data) {
		switch data[index] {
		case '"':
			return index + 1, nil
		case '\\':
			if index+1 >= len(data) {
				return 0, fmt.Errorf("invalid JSON")
			}
			if data[index+1] == 'u' {
				if index+5 >= len(data) || !isJSONHex(data[index+2:index+6]) {
					return 0, fmt.Errorf("invalid JSON")
				}
				index += 6
				continue
			}
			index += 2
		default:
			if data[index] < 0x20 {
				return 0, fmt.Errorf("invalid JSON")
			}
			index++
		}
	}
	return 0, fmt.Errorf("invalid JSON")
}

func skipJSONLiteral(data []byte, index int, literal string) (int, error) {
	if index+len(literal) > len(data) || string(data[index:index+len(literal)]) != literal {
		return 0, fmt.Errorf("invalid JSON")
	}
	return index + len(literal), nil
}

func skipJSONNumber(data []byte, index int) (int, error) {
	start := index
	if index < len(data) && data[index] == '-' {
		index++
	}
	for index < len(data) {
		switch data[index] {
		case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '.', 'e', 'E', '+', '-':
			index++
		default:
			if index == start {
				return 0, fmt.Errorf("invalid JSON")
			}
			return index, nil
		}
	}
	if index == start {
		return 0, fmt.Errorf("invalid JSON")
	}
	return index, nil
}

func skipJSONSpace(data []byte, index int) int {
	for index < len(data) {
		switch data[index] {
		case ' ', '\n', '\r', '\t':
			index++
		default:
			return index
		}
	}
	return index
}

func isJSONHex(data []byte) bool {
	if len(data) != 4 {
		return false
	}
	for _, value := range data {
		switch {
		case value >= '0' && value <= '9', value >= 'a' && value <= 'f', value >= 'A' && value <= 'F':
		default:
			return false
		}
	}
	return true
}
