package httpapi

import (
	"encoding/base64"
	"errors"
	"strconv"
)

func encodeCursor(sequence int64) string {
	if sequence <= 0 {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(sequence, 10)))
}

func decodeCursor(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return 0, errors.New("cursor is invalid")
	}
	sequence, err := strconv.ParseInt(string(decoded), 10, 64)
	if err != nil || sequence <= 0 {
		return 0, errors.New("cursor is invalid")
	}
	return sequence, nil
}
