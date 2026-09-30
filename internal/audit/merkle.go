package audit

import (
	"crypto/sha256"
	"encoding/hex"
)

// MerkleRoot hashes canonical record bytes with separate leaf and branch
// domains. An unpaired node is duplicated at each level.
func MerkleRoot(records [][]byte) []byte {
	if len(records) == 0 {
		empty := sha256.Sum256([]byte{0})
		return empty[:]
	}
	level := make([][]byte, len(records))
	for i, record := range records {
		input := make([]byte, 1, len(record)+1)
		input[0] = 0
		input = append(input, record...)
		hash := sha256.Sum256(input)
		level[i] = hash[:]
	}
	for len(level) > 1 {
		next := make([][]byte, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			right := level[i]
			if i+1 < len(level) {
				right = level[i+1]
			}
			input := make([]byte, 1, 1+len(level[i])+len(right))
			input[0] = 1
			input = append(input, level[i]...)
			input = append(input, right...)
			hash := sha256.Sum256(input)
			next = append(next, hash[:])
		}
		level = next
	}
	return append([]byte(nil), level[0]...)
}

func formatMerkleRoot(root []byte) string { return "sha256:" + hex.EncodeToString(root) }
