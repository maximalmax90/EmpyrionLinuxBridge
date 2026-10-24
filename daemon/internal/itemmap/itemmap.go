package itemmap

import (
	"fmt"
	"os"
)

const headerSize = 9

func Read(path string) (map[string]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) < headerSize {
		return nil, fmt.Errorf("%s: файл короче заголовка (%d байт)", path, len(data))
	}

	result := make(map[string]int)
	offset := headerSize
	for offset < len(data) {
		nameLen := int(data[offset])
		offset++
		if offset+nameLen+2 > len(data) {
			break
		}
		name := string(data[offset : offset+nameLen])
		offset += nameLen
		id := int(data[offset]) | int(data[offset+1])<<8
		offset += 2
		result[name] = id
	}
	return result, nil
}
